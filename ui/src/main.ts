import { callRPC } from './api/rpc';
import { fetchText } from './api/client';
import { extractMetrics, parseMetrics } from './api/metrics';
import { initControlPanel } from './components/ControlPanel';
import { createConnectionTable } from './components/ConnectionTable';
import { createUpstreamCard } from './components/UpstreamCard';
import { renderStatusCard } from './components/StatusCard';
import { createToastManager } from './components/Toast';
import { connectStatusSocket } from './websocket/status';
import { createInitialState, Store } from './state/store';
import type {
  ConnectionEntry,
  RawConnectionEntry,
  StatusResponse,
  UpstreamMetrics,
  WSMessage
} from './types';
import { clearChildren, qs } from './utils/dom';

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

  let upstreamCards = new Map<string, ReturnType<typeof createUpstreamCard>>();

  const controlPanel = initControlPanel({
    container: qs<HTMLElement>(document, '#modeTransition'),
    hintEl: qs<HTMLElement>(document, '#controlHint'),
    authButton: qs<HTMLButtonElement>(document, '#authButton'),
    store,
    toast,
    onApply: async (mode, tag) => {
      const params = { mode, tag: tag || '' };
      const result = await callRPC<unknown>(token, 'SetUpstream', params);
      if (result.ok) {
        await loadStatus();
        return true;
      }
      toast.show(result.error || 'Request failed.', 'error');
      return false;
    },
    onRestart: async () => {
      const result = await callRPC<unknown>(token, 'Restart', {});
      if (!result.ok) {
        toast.show(result.error || 'Request failed.', 'error');
        return false;
      }
      return true;
    }
  });

  function updateStatusCard(): void {
    const state = store.getState();
    statusCard({
      mode: state.mode,
      activeUpstream: state.activeUpstream,
      tcp: state.counts.tcp,
      udp: state.counts.udp,
      memoryBytes: state.memoryBytes
    });

    upstreamSummary.textContent = `${state.upstreams.length} upstreams`;
    tcpSummary.textContent = `${state.counts.tcp} active`;
    udpSummary.textContent = `${state.counts.udp} active`;

    const titleBits = ['fbforward', state.mode];
    if (state.activeUpstream) {
      titleBits.push(state.activeUpstream);
    }
    document.title = titleBits.join(' - ');
  }

  function rebuildUpstreamGrid(): void {
    const state = store.getState();
    upstreamCards = new Map();
    clearChildren(upstreamGrid);
    for (const upstream of state.upstreams) {
      const card = createUpstreamCard(upstream);
      upstreamCards.set(upstream.tag, card);
      upstreamGrid.appendChild(card.element);
    }
    controlPanel.setUpstreams(state.upstreams.map(up => up.tag));
    updateUpstreamCards();
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
      }
    });
    controlPanel.setMode(data.mode, data.active_upstream || null, false);
    rebuildUpstreamGrid();
    updateStatusCard();
    updateTables();
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
        memoryBytes: snapshot.memoryBytes
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
