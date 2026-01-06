import { callRPC } from './api/rpc';
import { fetchJSON, fetchText } from './api/client';
import { extractMetrics, parseMetrics } from './api/metrics';
import { createConnectionTable } from './components/ConnectionTable';
import { createUpstreamCard } from './components/UpstreamCard';
import { renderStatusCard } from './components/StatusCard';
import { createToastManager } from './components/Toast';
import { connectStatusSocket } from './websocket/status';
import { createInitialState, Store } from './state/store';
import type {
  ConnectionEntry,
  Mode,
  RawConnectionEntry,
  StatusResponse,
  UpstreamMetrics,
  IdentityResponse,
  WSMessage
} from './types';
import { clearChildren, qs } from './utils/dom';
import { formatBytes, formatDuration } from './utils/format';

const storedToken = localStorage.getItem('fbforward_token') || '';
if (!storedToken) {
  window.location.href = '/auth';
} else {
  startApp(storedToken);
}

function startApp(token: string) {
  const store = new Store(createInitialState(token));

  const statusCard = renderStatusCard(qs<HTMLElement>(document, '#statusCard'));
  const upstreamGrid = qs<HTMLElement>(document, '#upstreamGrid');
  const upstreamSummary = qs<HTMLElement>(document, '#upstreamSummary');
  const tcpSummary = qs<HTMLElement>(document, '#tcpSummary');
  const udpSummary = qs<HTMLElement>(document, '#udpSummary');
  const tcpTable = createConnectionTable(qs<HTMLElement>(document, '#tcpTable'));
  const udpTable = createConnectionTable(qs<HTMLElement>(document, '#udpTable'));
  const toast = createToastManager(qs<HTMLElement>(document, '#toastRegion'));
  const restartButton = qs<HTMLButtonElement>(document, '#restartButton');
  const modeButtons = Array.from(
    document.querySelectorAll<HTMLButtonElement>('.segmented-button')
  );

  let upstreamCards = new Map<string, ReturnType<typeof createUpstreamCard>>();
  restartButton.addEventListener('click', async () => {
    restartButton.disabled = true;
    const result = await callRPC<unknown>(token, 'Restart', {});
    if (!result.ok) {
      toast.show(result.error || 'Request failed.', 'error');
    } else {
      toast.show('Restart requested.', 'warning');
    }
    restartButton.disabled = false;
  });

  modeButtons.forEach(button => {
    button.addEventListener('click', () => {
      const mode = button.dataset.mode === 'manual' ? 'manual' : 'auto';
      setModeUI(mode, true);
    });
  });

  function updateStatusCard(): void {
    const state = store.getState();
    statusCard({
      mode: state.control.mode,
      activeUpstream: state.activeUpstream,
      tcp: state.counts.tcp,
      udp: state.counts.udp,
      memoryBytes: state.memoryBytes
    });
    updateHeaderStats();

    upstreamSummary.textContent = `${state.upstreams.length} upstreams`;
    tcpSummary.textContent = `${state.counts.tcp} active`;
    udpSummary.textContent = `${state.counts.udp} active`;

    if (state.hostname) {
      document.title = `fbforward - ${state.hostname}`;
    }
  }

  function rebuildUpstreamGrid(): void {
    const state = store.getState();
    upstreamCards = new Map();
    clearChildren(upstreamGrid);
    for (const upstream of state.upstreams) {
      const card = createUpstreamCard(upstream);
      card.element.addEventListener('click', () => {
        handleUpstreamSelect(upstream.tag);
      });
      upstreamCards.set(upstream.tag, card);
      upstreamGrid.appendChild(card.element);
    }
    updateUpstreamCards();
    updateUpstreamInteractivity();
  }

  function updateUpstreamCards(): void {
    const state = store.getState();
    const metrics = state.metrics;
    const best = computeBestMetrics(metrics);

    for (const [tag, card] of upstreamCards.entries()) {
      const snapshot = metrics[tag] || defaultMetrics(tag, state.activeUpstream);
      card.update(snapshot, {
        bestRtt: best.bestRtt === tag,
        bestLoss: best.bestLoss === tag,
        bestScore: best.bestScore === tag
      });
    }
    updateUpstreamInteractivity();
  }

  function updateModeSwitch(): void {
    const current = store.getState().control.mode;
    for (const button of modeButtons) {
      const active = button.dataset.mode === current;
      button.classList.toggle('active', active);
      button.setAttribute('aria-pressed', active ? 'true' : 'false');
    }
  }

  function updateUpstreamInteractivity(): void {
    const state = store.getState();
    const isManual = state.control.mode === 'manual';
    for (const [tag, card] of upstreamCards.entries()) {
      card.element.classList.toggle('selectable', isManual);
      card.element.classList.toggle('disabled', !isManual);
      card.element.classList.toggle('selected', isManual && state.control.selectedUpstream === tag);
    }
  }

  function setModeUI(mode: Mode, apply: boolean): void {
    const state = store.getState();
    let selected = state.control.selectedUpstream;
    if (mode === 'manual') {
      if (!selected) {
        selected = state.activeUpstream || state.upstreams[0]?.tag || null;
      }
    } else {
      selected = null;
    }
    store.setState({
      control: {
        ...state.control,
        mode,
        selectedUpstream: selected
      }
    });
    updateModeSwitch();
    updateUpstreamInteractivity();
    updateStatusCard();
    if (apply) {
      void applyMode(mode, selected);
    }
  }

  async function applyMode(mode: Mode, selected: string | null): Promise<void> {
    const params = { mode, tag: selected || '' };
    const result = await callRPC<unknown>(token, 'SetUpstream', params);
    if (!result.ok) {
      toast.show(result.error || 'Request failed.', 'error');
    }
  }

  function handleUpstreamSelect(tag: string): void {
    const state = store.getState();
    if (state.control.mode !== 'manual') {
      return;
    }
    store.setState({
      control: {
        ...state.control,
        selectedUpstream: tag
      }
    });
    updateUpstreamInteractivity();
    void applyMode('manual', tag);
  }

  function updateTables(): void {
    const state = store.getState();
    tcpTable(Array.from(state.connections.tcp.values()));
    udpTable(Array.from(state.connections.udp.values()));
  }

  async function loadStatus(): Promise<void> {
    const resp = await callRPC<StatusResponse>(token, 'GetStatus', {});
    if (!resp.ok || !resp.result) {
      toast.show(resp.error || 'Unable to load status.', 'error');
      return;
    }
    const data = resp.result;
    store.setState({
      upstreams: data.upstreams,
      mode: data.mode,
      activeUpstream: data.active_upstream,
      counts: {
        tcp: data.counts.tcp_active,
        udp: data.counts.udp_active
      },
      control: {
        ...store.getState().control,
        mode: data.mode,
        selectedUpstream: data.active_upstream || null
      }
    });
    rebuildUpstreamGrid();
    updateModeSwitch();
    updateStatusCard();
    updateTables();
  }

  async function loadIdentity(): Promise<void> {
    try {
      const resp = await fetchJSON<{ ok: boolean; result?: IdentityResponse; error?: string }>(
        '/identity',
        token
      );
      if (!resp.ok || !resp.result) {
        return;
      }
      store.setState({
        hostname: resp.result.hostname || '',
        hostIPs: resp.result.ips || []
      });
      updateHeaderIdentity();
      updateStatusCard();
    } catch (err) {
      // ignore identity load failures
    }
  }

  function updateHeaderIdentity(): void {
    const state = store.getState();
    const nameEl = qs<HTMLElement>(document, '#hostName');
    const ipEl = qs<HTMLElement>(document, '#hostIPs');
    nameEl.textContent = state.hostname || '-';
    if (state.hostIPs.length === 0) {
      ipEl.textContent = '-';
    } else {
      ipEl.textContent = state.hostIPs.join(', ');
    }
  }

  function updateHeaderStats(): void {
    const state = store.getState();
    const uptimeEl = qs<HTMLElement>(document, '#uptimeValue');
    const totalUpEl = qs<HTMLElement>(document, '#totalUpValue');
    const totalDownEl = qs<HTMLElement>(document, '#totalDownValue');
    uptimeEl.textContent = formatDuration(state.uptimeSeconds);
    totalUpEl.textContent = formatBytes(state.totalBytesUp);
    totalDownEl.textContent = formatBytes(state.totalBytesDown);
  }

  async function pollMetrics(): Promise<void> {
    try {
      const text = await fetchText('/metrics', token);
      const parsed = parseMetrics(text);
      const snapshot = extractMetrics(parsed);

      store.setState({
        mode: snapshot.mode,
        activeUpstream: snapshot.activeUpstream,
        counts: snapshot.counts,
        metrics: snapshot.upstreams,
        memoryBytes: snapshot.memoryBytes,
        uptimeSeconds: snapshot.uptimeSeconds,
        totalBytesUp: snapshot.totalBytesUp,
        totalBytesDown: snapshot.totalBytesDown
      });
      updateStatusCard();
      updateUpstreamCards();
    } catch (err) {
      // ignore polling errors
    }
  }

  function handleStatusMessage(message: WSMessage): void {
    store.update(state => {
      if (message.type === 'snapshot') {
        state.connections.tcp = new Map(
          (message.tcp || []).map(entry => [entry.id, normalizeEntry(entry)])
        );
        state.connections.udp = new Map(
          (message.udp || []).map(entry => [entry.id, normalizeEntry(entry)])
        );
        return;
      }
      if (message.type === 'add' || message.type === 'update') {
        if (!message.entry) {
          return;
        }
        const entry = normalizeEntry(message.entry);
        if (entry.kind === 'tcp') {
          state.connections.tcp.set(entry.id, entry);
        } else {
          state.connections.udp.set(entry.id, entry);
        }
        return;
      }
      if (message.type === 'remove' && message.id && message.kind) {
        if (message.kind === 'tcp') {
          state.connections.tcp.delete(message.id);
        } else {
          state.connections.udp.delete(message.id);
        }
      }
    });
    updateTables();
  }

  connectStatusSocket({
    token,
    onMessage: handleStatusMessage
  });

  loadStatus();
  loadIdentity();
  pollMetrics();
  window.setInterval(pollMetrics, 1000);
}

function normalizeEntry(raw: RawConnectionEntry): ConnectionEntry {
  return {
    id: raw.id,
    clientAddr: raw.client_addr,
    upstream: raw.upstream,
    bytesUp: raw.bytes_up,
    bytesDown: raw.bytes_down,
    lastActivity: raw.last_activity,
    age: raw.age,
    kind: raw.kind
  };
}

function defaultMetrics(tag: string, activeTag: string): UpstreamMetrics {
  return {
    rtt: 0,
    jitter: 0,
    loss: 0,
    score: 0,
    unusable: true,
    active: tag === activeTag
  };
}

function computeBestMetrics(metrics: Record<string, UpstreamMetrics>) {
  let bestRtt = '';
  let bestLoss = '';
  let bestScore = '';
  let minRtt = Number.POSITIVE_INFINITY;
  let minLoss = Number.POSITIVE_INFINITY;
  let maxScore = Number.NEGATIVE_INFINITY;

  for (const [tag, snapshot] of Object.entries(metrics)) {
    if (snapshot.unusable) {
      continue;
    }
    if (snapshot.rtt < minRtt) {
      minRtt = snapshot.rtt;
      bestRtt = tag;
    }
    if (snapshot.loss < minLoss) {
      minLoss = snapshot.loss;
      bestLoss = tag;
    }
    if (snapshot.score > maxScore) {
      maxScore = snapshot.score;
      bestScore = tag;
    }
  }

  return { bestRtt, bestLoss, bestScore };
}
