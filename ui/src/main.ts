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
  const connectionsSummary = qs<HTMLElement>(document, '#connectionsSummary');
  const connectionTable = createConnectionTable(qs<HTMLElement>(document, '#connectionTable'));
  const toast = createToastManager(qs<HTMLElement>(document, '#toastRegion'));
  const restartButton = qs<HTMLButtonElement>(document, '#restartButton');
  const sortButtons = Array.from(
    document.querySelectorAll<HTMLButtonElement>('.sort-button')
  );
  const modeButtons = Array.from(
    document.querySelectorAll<HTMLButtonElement>('.segmented-button[data-mode]')
  );
  const intervalButtons = Array.from(
    document.querySelectorAll<HTMLButtonElement>('.polling-button')
  );
  let pollTimer: number | null = null;
  let pollIntervalMs = 1000;

  let upstreamCards = new Map<string, ReturnType<typeof createUpstreamCard>>();
  const entryOrder = new Map<string, number>();
  let entrySeq = 0;
  const sortState: SortState = { key: 'client', direction: 'asc' };
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

  intervalButtons.forEach(button => {
    button.addEventListener('click', () => {
      const value = Number.parseInt(button.dataset.interval || '', 10);
      if (!Number.isFinite(value) || value <= 0) {
        return;
      }
      setPollInterval(value);
    });
  });

  function setPollInterval(seconds: number): void {
    pollIntervalMs = seconds * 1000;
    updatePollIntervalUI();
    startPolling();
  }

  function updatePollIntervalUI(): void {
    for (const button of intervalButtons) {
      const value = Number.parseInt(button.dataset.interval || '', 10);
      const active = value * 1000 === pollIntervalMs;
      button.classList.toggle('active', active);
      button.setAttribute('aria-pressed', active ? 'true' : 'false');
    }
  }

  sortButtons.forEach(button => {
    button.addEventListener('click', () => {
      const key = button.dataset.sort as SortKey | undefined;
      if (!key) {
        return;
      }
      if (sortState.key === key) {
        sortState.direction = sortState.direction === 'asc' ? 'desc' : 'asc';
      } else {
        sortState.key = key;
        sortState.direction = 'asc';
      }
      updateSortIndicators();
      updateTables();
    });
  });

  updateSortIndicators();

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

  function updateSortIndicators(): void {
    for (const button of sortButtons) {
      const key = button.dataset.sort as SortKey | undefined;
      if (!key) {
        continue;
      }
      const active = key === sortState.key;
      button.classList.toggle('is-active', active);
      const indicator = button.querySelector<HTMLElement>('.sort-indicator');
      if (indicator) {
        indicator.textContent = active
          ? sortState.direction === 'asc'
            ? '\u25B2'
            : '\u25BC'
          : '';
      }
      const th = button.closest('th');
      if (th) {
        th.setAttribute(
          'aria-sort',
          active ? (sortState.direction === 'asc' ? 'ascending' : 'descending') : 'none'
        );
      }
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
    const entries = Array.from(state.connections.tcp.values()).concat(
      Array.from(state.connections.udp.values())
    );
    for (const entry of entries) {
      if (!entryOrder.has(entry.id)) {
        entryOrder.set(entry.id, entrySeq);
        entrySeq += 1;
      }
    }
    entries.sort((a, b) => compareEntries(a, b, sortState, entryOrder));
    connectionTable(entries);
    connectionsSummary.textContent = `${entries.length} active`;
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
    if (message.type === 'snapshot') {
      const currentIds = new Set<string>();
      for (const entry of message.tcp || []) {
        currentIds.add(entry.id);
      }
      for (const entry of message.udp || []) {
        currentIds.add(entry.id);
      }
      for (const id of entryOrder.keys()) {
        if (!currentIds.has(id)) {
          entryOrder.delete(id);
        }
      }
    }
    if (message.type === 'remove' && message.id) {
      entryOrder.delete(message.id);
    }
    updateTables();
  }

  connectStatusSocket({
    token,
    onMessage: handleStatusMessage
  });

  loadStatus();
  loadIdentity();
  updatePollIntervalUI();
  function startPolling(): void {
    if (pollTimer !== null) {
      window.clearInterval(pollTimer);
    }
    pollTimer = window.setInterval(pollMetrics, pollIntervalMs);
  }

  pollMetrics();
  startPolling();
}

type SortKey = 'protocol' | 'client' | 'upstream' | 'up' | 'down' | 'last' | 'age';
type SortDirection = 'asc' | 'desc';

interface SortState {
  key: SortKey;
  direction: SortDirection;
}

interface ParsedHost {
  kind: 'ipv4' | 'ipv6' | 'name';
  parts: number[];
  text: string;
}

interface ParsedClient {
  host: ParsedHost;
  port: number;
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

function compareEntries(
  a: ConnectionEntry,
  b: ConnectionEntry,
  sortState: SortState,
  entryOrder: Map<string, number>
): number {
  const primary = compareByKey(a, b, sortState.key);
  if (primary !== 0) {
    return sortState.direction === 'asc' ? primary : -primary;
  }
  const orderA = entryOrder.get(a.id) ?? 0;
  const orderB = entryOrder.get(b.id) ?? 0;
  return orderA - orderB;
}

function compareByKey(a: ConnectionEntry, b: ConnectionEntry, key: SortKey): number {
  switch (key) {
    case 'protocol':
      return a.kind.localeCompare(b.kind);
    case 'client': {
      const parsedA = parseClientAddress(a.clientAddr);
      const parsedB = parseClientAddress(b.clientAddr);
      const hostCompare = compareHosts(parsedA.host, parsedB.host);
      if (hostCompare !== 0) {
        return hostCompare;
      }
      return compareNumber(parsedA.port, parsedB.port);
    }
    case 'upstream':
      return a.upstream.localeCompare(b.upstream);
    case 'up':
      return compareNumber(a.bytesUp, b.bytesUp);
    case 'down':
      return compareNumber(a.bytesDown, b.bytesDown);
    case 'last':
      return compareNumber(a.lastActivity, b.lastActivity);
    case 'age':
      return compareNumber(a.age, b.age);
    default:
      return 0;
  }
}

function compareNumber(a: number, b: number): number {
  if (a === b) {
    return 0;
  }
  return a < b ? -1 : 1;
}

function parseClientAddress(value: string): ParsedClient {
  const trimmed = value.trim();
  let hostPart = trimmed;
  let port = 0;

  if (trimmed.startsWith('[')) {
    const end = trimmed.indexOf(']');
    if (end > 0) {
      hostPart = trimmed.slice(1, end);
      const portPart = trimmed.slice(end + 1);
      if (portPart.startsWith(':')) {
        port = parsePort(portPart.slice(1));
      }
    }
  } else {
    const lastColon = trimmed.lastIndexOf(':');
    if (lastColon > -1) {
      const hostCandidate = trimmed.slice(0, lastColon);
      const portCandidate = trimmed.slice(lastColon + 1);
      if (hostCandidate.includes(':')) {
        hostPart = hostCandidate;
        port = parsePort(portCandidate);
      } else if (hostCandidate.length > 0) {
        hostPart = hostCandidate;
        port = parsePort(portCandidate);
      }
    }
  }

  hostPart = hostPart.split('%')[0];
  return {
    host: parseHost(hostPart),
    port
  };
}

function parsePort(value: string): number {
  const parsed = Number.parseInt(value, 10);
  return Number.isFinite(parsed) ? parsed : 0;
}

function parseHost(value: string): ParsedHost {
  const cleaned = value.trim().toLowerCase();
  const ipv4 = parseIPv4(cleaned);
  if (ipv4) {
    return { kind: 'ipv4', parts: ipv4, text: cleaned };
  }
  const ipv6 = parseIPv6(cleaned);
  if (ipv6) {
    return { kind: 'ipv6', parts: ipv6, text: cleaned };
  }
  return { kind: 'name', parts: [], text: cleaned };
}

function compareHosts(a: ParsedHost, b: ParsedHost): number {
  const rank = { ipv4: 0, ipv6: 1, name: 2 };
  if (rank[a.kind] !== rank[b.kind]) {
    return rank[a.kind] - rank[b.kind];
  }
  if (a.kind === 'name') {
    return a.text.localeCompare(b.text);
  }
  const len = Math.max(a.parts.length, b.parts.length);
  for (let i = 0; i < len; i += 1) {
    const diff = compareNumber(a.parts[i] ?? 0, b.parts[i] ?? 0);
    if (diff !== 0) {
      return diff;
    }
  }
  return 0;
}

function parseIPv4(value: string): number[] | null {
  const parts = value.split('.');
  if (parts.length !== 4) {
    return null;
  }
  const nums: number[] = [];
  for (const part of parts) {
    if (part === '') {
      return null;
    }
    const parsed = Number.parseInt(part, 10);
    if (!Number.isFinite(parsed) || parsed < 0 || parsed > 255) {
      return null;
    }
    nums.push(parsed);
  }
  return nums;
}

function parseIPv6(value: string): number[] | null {
  if (!value.includes(':') || value.includes('.')) {
    return null;
  }
  const segments = value.split('::');
  if (segments.length > 2) {
    return null;
  }
  const head = segments[0] ? segments[0].split(':') : [];
  const tail = segments[1] ? segments[1].split(':') : [];
  if (segments.length === 1 && head.length !== 8) {
    return null;
  }
  const total = head.length + tail.length;
  if (total > 8) {
    return null;
  }
  const zeros = segments.length === 2 ? 8 - total : 0;
  const nums: number[] = [];
  for (const part of head) {
    const parsed = parseHex(part);
    if (parsed === null) {
      return null;
    }
    nums.push(parsed);
  }
  for (let i = 0; i < zeros; i += 1) {
    nums.push(0);
  }
  for (const part of tail) {
    const parsed = parseHex(part);
    if (parsed === null) {
      return null;
    }
    nums.push(parsed);
  }
  return nums.length === 8 ? nums : null;
}

function parseHex(value: string): number | null {
  if (value === '') {
    return null;
  }
  const parsed = Number.parseInt(value, 16);
  if (!Number.isFinite(parsed) || parsed < 0 || parsed > 0xffff) {
    return null;
  }
  return parsed;
}
