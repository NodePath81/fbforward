import { callRPC, getRuntimeConfig } from './api/rpc';
import { fetchJSON, fetchText } from './api/client';
import { extractMetrics, parseMetrics } from './api/metrics';
import { createChart, type ChartHandle, type ChartSeries } from './components/Chart';
import { createConnectionTable } from './components/ConnectionTable';
import { createUpstreamCard } from './components/UpstreamCard';
import { renderStatusCard } from './components/StatusCard';
import { createToastManager } from './components/Toast';
import { historyStore, type SessionHistoryEntry } from './state/history';
import { createInitialState, Store } from './state/store';
import { timeSeriesStore } from './state/timeseries';
import type {
  ConnectionEntry,
  IdentityResponse,
  Mode,
  RawConnectionEntry,
  StatusResponse,
  UpstreamMetrics,
  WSMessage
} from './types';
import { clearChildren, createEl, qs } from './utils/dom';
import { formatBytes, formatBytesRate, formatDuration, formatMs, formatPercent, formatScore } from './utils/format';
import { connectStatusSocket } from './websocket/status';

const storedToken = localStorage.getItem('fbforward_token') || '';
if (!storedToken) {
  window.location.href = '/auth';
} else {
  startApp(storedToken);
}

type Page = 'dashboard' | 'graph' | 'history' | 'config';
type ConnectionSortKey = 'protocol' | 'client' | 'upstream' | 'up' | 'down' | 'last' | 'age';
type SessionSortKey = 'id' | 'protocol' | 'client' | 'upstream' | 'start' | 'end' | 'up' | 'down';
type SortDirection = 'asc' | 'desc';

interface ConnectionSortState {
  key: ConnectionSortKey;
  direction: SortDirection;
}

interface SessionSortState {
  key: SessionSortKey;
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

function startApp(token: string) {
  const store = new Store(createInitialState(token));

  const statusCard = renderStatusCard(qs<HTMLElement>(document, '#statusCard'));
  const upstreamGrid = qs<HTMLElement>(document, '#upstreamGrid');
  const upstreamSummary = qs<HTMLElement>(document, '#upstreamSummary');
  const connectionsSummary = qs<HTMLElement>(document, '#connectionsSummary');
  const connectionTable = createConnectionTable(qs<HTMLElement>(document, '#connectionTable'));
  const connectionSearch = qs<HTMLInputElement>(document, '#connectionSearch');
  const sessionSearch = qs<HTMLInputElement>(document, '#sessionSearch');
  const toast = createToastManager(qs<HTMLElement>(document, '#toastRegion'));
  const restartButton = qs<HTMLButtonElement>(document, '#restartButton');
  const pollStatus = qs<HTMLElement>(document, '#pollStatus');
  const connectionSortButtons = Array.from(
    document.querySelectorAll<HTMLButtonElement>('#page-dashboard .sort-button[data-sort]')
  );
  const sessionSortButtons = Array.from(
    document.querySelectorAll<HTMLButtonElement>('.session-sort-button[data-sort]')
  );
  const modeButtons = Array.from(
    document.querySelectorAll<HTMLButtonElement>('.segmented-button[data-mode]')
  );
  const intervalButtons = Array.from(
    document.querySelectorAll<HTMLButtonElement>('.polling-button')
  );
  const navLinks = Array.from(document.querySelectorAll<HTMLAnchorElement>('.page-nav-link'));
  const pages = Array.from(document.querySelectorAll<HTMLElement>('.page'));
  const sessionHistoryTable = qs<HTMLTableSectionElement>(document, '#sessionHistoryTable');
  const configTree = qs<HTMLElement>(document, '#configTree');
  const upstreamDetailsModal = qs<HTMLElement>(document, '#upstreamDetailsModal');
  const rttChartContainer = qs<HTMLElement>(document, '#rttChartContainer');
  const scoreChartContainer = qs<HTMLElement>(document, '#scoreChartContainer');
  const trafficChartContainer = qs<HTMLElement>(document, '#trafficChartContainer');

  let pollTimer: number | null = null;
  let pollInProgress = false;
  let statusSocket: ReturnType<typeof connectStatusSocket> | null = null;
  let currentPage: Page = 'dashboard';
  let openUpstreamTag: string | null = null;
  let pollIntervalMs = getDefaultPollInterval(intervalButtons);
  let upstreamCards = new Map<string, ReturnType<typeof createUpstreamCard>>();
  let rttChart: ChartHandle | null = null;
  let scoreChart: ChartHandle | null = null;
  let trafficChart: ChartHandle | null = null;
  let prevBytesUp: number | null = null;
  let prevBytesDown: number | null = null;
  let prevTrafficTs: number | null = null;

  const entryOrder = new Map<string, number>();
  let entrySeq = 0;
  const connectionSortState: ConnectionSortState = { key: 'client', direction: 'asc' };
  const sessionSortState: SessionSortState = { key: 'end', direction: 'desc' };
  const palette = [
    'var(--color-accent)',
    '#0e8ead',
    '#1e8449',
    '#d68910',
    '#7d5fff',
    '#c0392b',
    '#7f8c8d'
  ];
  const tagColors = new Map<string, string>();

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

  connectionSortButtons.forEach(button => {
    button.addEventListener('click', () => {
      const key = button.dataset.sort as ConnectionSortKey | undefined;
      if (!key) {
        return;
      }
      if (connectionSortState.key === key) {
        connectionSortState.direction = connectionSortState.direction === 'asc' ? 'desc' : 'asc';
      } else {
        connectionSortState.key = key;
        connectionSortState.direction = 'asc';
      }
      updateSortIndicators(connectionSortButtons, connectionSortState.key, connectionSortState.direction);
      updateTables();
    });
  });

  sessionSortButtons.forEach(button => {
    button.addEventListener('click', () => {
      const key = button.dataset.sort as SessionSortKey | undefined;
      if (!key) {
        return;
      }
      if (sessionSortState.key === key) {
        sessionSortState.direction = sessionSortState.direction === 'asc' ? 'desc' : 'asc';
      } else {
        sessionSortState.key = key;
        sessionSortState.direction = 'asc';
      }
      updateSortIndicators(sessionSortButtons, sessionSortState.key, sessionSortState.direction);
      renderSessionHistory();
    });
  });

  connectionSearch.addEventListener('input', () => {
    updateTables();
  });

  sessionSearch.addEventListener('input', () => {
    renderSessionHistory();
  });

  window.addEventListener('hashchange', () => {
    setActivePage(resolvePageFromHash());
  });

  document.addEventListener('keydown', event => {
    if (event.key === 'Escape' && !upstreamDetailsModal.classList.contains('hidden')) {
      hideUpstreamDetails();
    }
  });

  window.setInterval(() => {
    if (currentPage === 'dashboard') {
      updateTables();
    }
  }, 1000);

  function setPollInterval(seconds: number): void {
    pollIntervalMs = seconds * 1000;
    localStorage.setItem('fbforward_poll_interval_ms', pollIntervalMs.toString());
    updatePollIntervalUI(intervalButtons, pollIntervalMs);
    startPolling();
    statusSocket?.updateSnapshotInterval(pollIntervalMs);
  }

  function resolvePageFromHash(): Page {
    const hash = window.location.hash.replace(/^#\/?/, '');
    if (hash === 'graph') {
      return 'graph';
    }
    if (hash === 'history') {
      return 'history';
    }
    if (hash === 'config') {
      return 'config';
    }
    return 'dashboard';
  }

  function setActivePage(page: Page): void {
    currentPage = page;
    for (const el of pages) {
      const isActive = el.id === `page-${page}`;
      el.classList.toggle('hidden', !isActive);
    }
    for (const link of navLinks) {
      link.classList.toggle('active', link.dataset.page === page);
    }
    if (page === 'graph') {
      renderGraphPage();
    }
    if (page === 'history') {
      renderSessionHistory();
    }
    if (page === 'config') {
      void loadRuntimeConfig();
    }
  }

  function updateStatusCard(): void {
    const state = store.getState();
    statusCard({
      mode: state.control.mode,
      activeUpstream: state.activeUpstream,
      tcp: state.connections.tcp.size,
      udp: state.connections.udp.size,
      memoryBytes: state.memoryBytes,
      goroutines: state.goroutines
    });
    updateHeaderStats();
    updatePollStatus();
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
      card.onDetails = () => {
        showUpstreamDetails(upstream.tag);
      };
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
    refreshUpstreamDetailsModal();
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
    const entries = Array.from(state.connections.tcp.values()).concat(
      Array.from(state.connections.udp.values())
    );
    const total = entries.length;
    for (const entry of entries) {
      if (!entryOrder.has(entry.id)) {
        entryOrder.set(entry.id, entrySeq);
        entrySeq += 1;
      }
    }
    const filtered = filterEntries(entries, connectionSearch.value);
    filtered.sort((a, b) => compareEntries(a, b, connectionSortState, entryOrder));
    connectionTable(filtered);
    connectionsSummary.textContent = `${total} active`;
  }

  function ensureCharts(): void {
    if (!rttChart) {
      rttChart = createChart(rttChartContainer, {
        emptyLabel: 'Waiting for RTT samples',
        yFormatter: value => formatMs(value, 1)
      });
    }
    if (!scoreChart) {
      scoreChart = createChart(scoreChartContainer, {
        emptyLabel: 'Waiting for score samples',
        yFormatter: value => formatScore(value),
        baselineZero: true
      });
    }
    if (!trafficChart) {
      trafficChart = createChart(trafficChartContainer, {
        emptyLabel: 'Waiting for traffic samples',
        yFormatter: value => formatBytesRate(value),
        baselineZero: true
      });
    }
  }

  function renderGraphPage(): void {
    ensureCharts();
    rttChart?.update(buildRTTChartSeries());
    scoreChart?.update(buildScoreChartSeries());
    trafficChart?.update(buildTrafficChartSeries());
  }

  function buildRTTChartSeries(): ChartSeries[] {
    return timeSeriesStore.getRTTSeries().map(series => ({
      label: `${series.tag} ${series.protocol.toUpperCase()}`,
      color: getSeriesColor(series.tag),
      dashed: series.protocol === 'udp',
      points: series.points
    }));
  }

  function buildTrafficChartSeries(): ChartSeries[] {
    const traffic = timeSeriesStore.getTrafficSeries();
    return [
      {
        label: 'Upload',
        color: 'var(--color-accent)',
        points: traffic.upload
      },
      {
        label: 'Download',
        color: '#0e8ead',
        points: traffic.download
      }
    ];
  }

  function buildScoreChartSeries(): ChartSeries[] {
    return timeSeriesStore.getScoreSeries().map(series => ({
      label: series.tag,
      color: getSeriesColor(series.tag),
      points: series.points
    }));
  }

  function getSeriesColor(tag: string): string {
    let color = tagColors.get(tag);
    if (color) {
      return color;
    }
    color = palette[tagColors.size % palette.length];
    tagColors.set(tag, color);
    return color;
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
        hostIPs: resp.result.ips || [],
        version: resp.result.version || ''
      });
      updateHeaderIdentity();
      updateStatusCard();
    } catch {
      // ignore identity load failures
    }
  }

  async function loadRuntimeConfig(): Promise<void> {
    configTree.textContent = 'Loading...';
    const resp = await getRuntimeConfig(token);
    if (!resp.ok || !resp.result) {
      configTree.textContent = resp.error || 'Unable to load runtime config.';
      return;
    }
    configTree.textContent = formatYaml(resp.result);
  }

  function renderSessionHistory(): void {
    const sessions = filterSessions(historyStore.getSessions(), sessionSearch.value).sort((a, b) =>
      compareSessionEntries(a, b, sessionSortState)
    );
    clearChildren(sessionHistoryTable);
    if (sessions.length === 0) {
      appendEmptyRow(sessionHistoryTable, 10, 'No session history yet');
      return;
    }
    for (const entry of sessions) {
      const row = createEl('tr');
      row.appendChild(createCell(entry.id));
      row.appendChild(createCell(entry.kind.toUpperCase()));
      row.appendChild(createCell(entry.clientAddr));
      row.appendChild(createCell(entry.upstream));
      row.appendChild(createCell(formatApproxTimestamp(entry.startTime, entry.startApproximate)));
      row.appendChild(createCell(formatApproxTimestamp(entry.endTime, entry.endApproximate)));
      row.appendChild(createCell(formatBytes(entry.bytesUp)));
      row.appendChild(createCell(formatBytes(entry.bytesDown)));
      row.appendChild(createCell(formatCount(entry.segmentsUp)));
      row.appendChild(createCell(formatCount(entry.segmentsDown)));
      sessionHistoryTable.appendChild(row);
    }
  }

  function appendEmptyRow(target: HTMLElement, colCount: number, label: string): void {
    const row = createEl('tr');
    const cell = createEl('td', 'empty-row', label);
    cell.setAttribute('colspan', String(colCount));
    row.appendChild(cell);
    target.appendChild(row);
  }

  function createCell(value: string): HTMLElement {
    const cell = createEl('td');
    cell.textContent = value;
    return cell;
  }

  function renderUpstreamDetailsModal(tag: string): void {
    const state = store.getState();
    const upstream = state.upstreams.find(item => item.tag === tag);
    const liveMetrics = state.metrics[tag];
    if (!upstream || !liveMetrics) {
      hideUpstreamDetails();
      return;
    }
    clearChildren(upstreamDetailsModal);
    const card = createEl('div', 'modal-card');
    const header = createEl('div', 'modal-header');
    const title = createEl('h3', 'modal-title', `Upstream: ${tag}`);
    const closeButton = createEl('button', 'modal-close', 'Close') as HTMLButtonElement;
    header.appendChild(title);
    header.appendChild(closeButton);
    card.appendChild(header);

    const meta = createEl('div', 'modal-meta');
    meta.appendChild(createDetailRow('Host', upstream.host));
    meta.appendChild(createDetailRow('Active IP', upstream.active_ip || '-'));
    meta.appendChild(createDetailRow('Reachable', liveMetrics.reachable ? 'yes' : 'no'));
    card.appendChild(meta);

    const metrics = createEl('div', 'modal-metrics');
    metrics.appendChild(createDetailRow('Score', formatScore(liveMetrics.score)));
    metrics.appendChild(createDetailRow('Score TCP', formatScore(liveMetrics.scoreTcp)));
    metrics.appendChild(createDetailRow('Score UDP', formatScore(liveMetrics.scoreUdp)));
    metrics.appendChild(createDetailRow('RTT', formatMs(liveMetrics.rtt)));
    metrics.appendChild(createDetailRow('RTT TCP', formatMs(liveMetrics.rttTcp)));
    metrics.appendChild(createDetailRow('RTT UDP', formatMs(liveMetrics.rttUdp)));
    metrics.appendChild(createDetailRow('Jitter', formatMs(liveMetrics.jitter)));
    metrics.appendChild(createDetailRow('Retrans rate', formatPercent(liveMetrics.retransRate, 2)));
    metrics.appendChild(createDetailRow('Loss rate', formatPercent(liveMetrics.lossRate, 2)));
    card.appendChild(metrics);

    closeButton.addEventListener('click', hideUpstreamDetails);
    upstreamDetailsModal.onclick = event => {
      if (event.target === upstreamDetailsModal) {
        hideUpstreamDetails();
      }
    };

    upstreamDetailsModal.appendChild(card);
  }

  function showUpstreamDetails(tag: string): void {
    const state = store.getState();
    const upstream = state.upstreams.find(item => item.tag === tag);
    if (!upstream) {
      return;
    }
    openUpstreamTag = tag;
    renderUpstreamDetailsModal(tag);
    upstreamDetailsModal.classList.remove('hidden');
  }

  function hideUpstreamDetails(): void {
    openUpstreamTag = null;
    upstreamDetailsModal.classList.add('hidden');
    clearChildren(upstreamDetailsModal);
  }

  function refreshUpstreamDetailsModal(): void {
    if (!openUpstreamTag) {
      return;
    }
    renderUpstreamDetailsModal(openUpstreamTag);
  }

  function createDetailRow(label: string, value: string): HTMLElement {
    const row = createEl('div', 'modal-row');
    const labelEl = createEl('span', 'modal-label', label);
    const valueEl = createEl('span', 'modal-value', value);
    row.appendChild(labelEl);
    row.appendChild(valueEl);
    return row;
  }

  function formatCount(value: number): string {
    if (!Number.isFinite(value)) {
      return '-';
    }
    return value.toLocaleString();
  }

  function updateHeaderIdentity(): void {
    const state = store.getState();
    const nameEl = qs<HTMLElement>(document, '#hostName');
    const ipEl = qs<HTMLElement>(document, '#hostIPs');
    const versionEl = qs<HTMLElement>(document, '#hostVersion');
    nameEl.textContent = state.hostname || '-';
    ipEl.textContent = state.hostIPs.length === 0 ? '-' : state.hostIPs.join(', ');
    versionEl.textContent = state.version ? `v${state.version}` : '-';
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

  function updatePollStatus(): void {
    const state = store.getState();
    if (state.pollErrors.metrics) {
      pollStatus.textContent = 'Connection issues';
      pollStatus.classList.add('error');
    } else {
      pollStatus.textContent = '';
      pollStatus.classList.remove('error');
    }
  }

  async function pollMetrics(): Promise<void> {
    try {
      const timeoutMs = Math.max(1000, pollIntervalMs - 1000);
      const text = await fetchText('/metrics', token, {}, timeoutMs);
      const parsed = parseMetrics(text);
      const snapshot = extractMetrics(parsed);
      const now = Date.now();

      for (const [tag, metrics] of Object.entries(snapshot.upstreams)) {
        timeSeriesStore.pushRTT(tag, 'tcp', now, metrics.rttTcp);
        timeSeriesStore.pushRTT(tag, 'udp', now, metrics.rttUdp);
        timeSeriesStore.pushScore(tag, now, metrics.score);
      }

      if (
        prevTrafficTs !== null &&
        prevBytesUp !== null &&
        prevBytesDown !== null &&
        now > prevTrafficTs
      ) {
        const deltaUp = snapshot.totalBytesUp - prevBytesUp;
        const deltaDown = snapshot.totalBytesDown - prevBytesDown;
        const deltaSec = (now - prevTrafficTs) / 1000;
        if (deltaUp >= 0 && deltaDown >= 0 && deltaSec > 0) {
          timeSeriesStore.pushTraffic(now, deltaUp / deltaSec, deltaDown / deltaSec);
        }
      }

      prevTrafficTs = now;
      prevBytesUp = snapshot.totalBytesUp;
      prevBytesDown = snapshot.totalBytesDown;

      store.setState({
        mode: snapshot.mode,
        activeUpstream: snapshot.activeUpstream,
        counts: snapshot.counts,
        metrics: snapshot.upstreams,
        memoryBytes: snapshot.memoryBytes,
        goroutines: snapshot.goroutines,
        uptimeSeconds: snapshot.uptimeSeconds,
        totalBytesUp: snapshot.totalBytesUp,
        totalBytesDown: snapshot.totalBytesDown
      });
      store.setState({
        pollErrors: {
          ...store.getState().pollErrors,
          metrics: null
        }
      });
      updateStatusCard();
      updateUpstreamCards();
      updatePollStatus();
      if (currentPage === 'graph') {
        renderGraphPage();
      }
    } catch (err) {
      store.setState({
        pollErrors: {
          ...store.getState().pollErrors,
          metrics: err instanceof Error ? err.message : 'Failed to fetch metrics'
        }
      });
      updatePollStatus();
    }
  }

  function trackSessionEntry(entry: RawConnectionEntry, approximateStart: boolean): void {
    const startTime = Date.now() - entry.age * 1000;
    historyStore.trackSessionStart(
      entry.id,
      entry.kind,
      entry.client_addr,
      entry.upstream,
      startTime,
      approximateStart
    );
    historyStore.trackSessionUpdate(
      entry.id,
      entry.bytes_up,
      entry.bytes_down,
      entry.segments_up ?? 0,
      entry.segments_down ?? 0
    );
  }

  function handleStatusMessage(message: WSMessage): void {
    if (message.type === 'queue_snapshot') {
      return;
    }

    if (message.type === 'test_history_event') {
      return;
    }

    if (message.type === 'error') {
      console.error('WebSocket error:', message.message || message.code || 'unknown error');
      return;
    }

    store.update(state => {
      if (message.type === 'connections_snapshot') {
        const nextTCP = new Map<string, ConnectionEntry>();
        const nextUDP = new Map<string, ConnectionEntry>();
        for (const raw of message.tcp || []) {
          nextTCP.set(raw.id, normalizeEntry(raw, state.connections.tcp.get(raw.id)));
        }
        for (const raw of message.udp || []) {
          nextUDP.set(raw.id, normalizeEntry(raw, state.connections.udp.get(raw.id)));
        }
        state.connections.tcp = nextTCP;
        state.connections.udp = nextUDP;
        return;
      }

      if ((message.type === 'add' || message.type === 'update') && message.entry) {
        const target =
          message.entry.kind === 'tcp' ? state.connections.tcp : state.connections.udp;
        const existing = target.get(message.entry.id);
        target.set(message.entry.id, normalizeEntry(message.entry, existing));
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

    if (message.type === 'connections_snapshot') {
      const currentIds = new Set<string>();
      for (const entry of message.tcp || []) {
        currentIds.add(entry.id);
        trackSessionEntry(entry, true);
      }
      for (const entry of message.udp || []) {
        currentIds.add(entry.id);
        trackSessionEntry(entry, true);
      }
      for (const id of entryOrder.keys()) {
        if (!currentIds.has(id)) {
          entryOrder.delete(id);
        }
      }
      updateTables();
      return;
    }

    if ((message.type === 'add' || message.type === 'update') && message.entry) {
      trackSessionEntry(message.entry, message.type === 'update');
      updateTables();
      return;
    }

    if (message.type === 'remove' && message.id) {
      historyStore.trackSessionEnd(message.id, message.timestamp);
      entryOrder.delete(message.id);
      if (currentPage === 'history') {
        renderSessionHistory();
      }
      updateTables();
    }
  }

  statusSocket = connectStatusSocket(
    {
      token,
      onMessage: handleStatusMessage
    },
    pollIntervalMs
  );

  updatePollIntervalUI(intervalButtons, pollIntervalMs);
  updateSortIndicators(connectionSortButtons, connectionSortState.key, connectionSortState.direction);
  updateSortIndicators(sessionSortButtons, sessionSortState.key, sessionSortState.direction);

  void loadStatus();
  void loadIdentity();
  setActivePage(resolvePageFromHash());

  function startPolling(): void {
    if (pollTimer !== null) {
      window.clearInterval(pollTimer);
    }
    pollTimer = window.setInterval(() => {
      void executePollCycle();
    }, pollIntervalMs);
  }

  async function executePollCycle(): Promise<void> {
    if (pollInProgress) {
      return;
    }
    pollInProgress = true;
    try {
      await pollMetrics();
    } finally {
      pollInProgress = false;
    }
  }

  void executePollCycle();
  startPolling();
}

function getDefaultPollInterval(buttons: HTMLButtonElement[]): number {
  const stored = localStorage.getItem('fbforward_poll_interval_ms');
  if (stored) {
    const parsed = Number.parseInt(stored, 10);
    if (Number.isFinite(parsed) && parsed > 0) {
      return parsed;
    }
  }
  for (const button of buttons) {
    if (button.getAttribute('aria-pressed') === 'true') {
      const value = Number.parseInt(button.dataset.interval || '', 10);
      if (Number.isFinite(value) && value > 0) {
        return value * 1000;
      }
    }
  }
  return 2000;
}

function updatePollIntervalUI(buttons: HTMLButtonElement[], pollIntervalMs: number): void {
  for (const button of buttons) {
    const value = Number.parseInt(button.dataset.interval || '', 10);
    const active = value * 1000 === pollIntervalMs;
    button.classList.toggle('active', active);
    button.setAttribute('aria-pressed', active ? 'true' : 'false');
  }
}

function updateSortIndicators(
  buttons: HTMLButtonElement[],
  activeKey: string,
  direction: SortDirection
): void {
  for (const button of buttons) {
    const key = button.dataset.sort;
    if (!key) {
      continue;
    }
    const active = key === activeKey;
    button.classList.toggle('is-active', active);
    const indicator = button.querySelector<HTMLElement>('.sort-indicator');
    if (indicator) {
      indicator.textContent = active ? (direction === 'asc' ? '\u25B2' : '\u25BC') : '';
    }
    const th = button.closest('th');
    if (th) {
      th.setAttribute('aria-sort', active ? (direction === 'asc' ? 'ascending' : 'descending') : 'none');
    }
  }
}

function normalizeEntry(raw: RawConnectionEntry, existing?: ConnectionEntry): ConnectionEntry {
  const createdAt = existing?.createdAt ?? Date.now() - raw.age * 1000;
  let rateUp = existing?.rateUp ?? Number.NaN;
  let rateDown = existing?.rateDown ?? Number.NaN;

  if (existing) {
    const elapsedMs = raw.last_activity - existing.lastActivity;
    if (elapsedMs > 0) {
      rateUp = computeRate(raw.bytes_up - existing.bytesUp, elapsedMs);
      rateDown = computeRate(raw.bytes_down - existing.bytesDown, elapsedMs);
    }
  }

  return {
    id: raw.id,
    clientAddr: raw.client_addr,
    port: raw.port,
    upstream: raw.upstream,
    bytesUp: raw.bytes_up,
    bytesDown: raw.bytes_down,
    segmentsUp: raw.segments_up ?? 0,
    segmentsDown: raw.segments_down ?? 0,
    lastActivity: raw.last_activity,
    age: raw.age,
    createdAt,
    rateUp,
    rateDown,
    kind: raw.kind
  };
}

function computeRate(deltaBytes: number, elapsedMs: number): number {
  if (!Number.isFinite(deltaBytes) || !Number.isFinite(elapsedMs) || elapsedMs <= 0) {
    return Number.NaN;
  }
  if (deltaBytes < 0) {
    return Number.NaN;
  }
  return deltaBytes / (elapsedMs / 1000);
}

function filterEntries(entries: ConnectionEntry[], query: string): ConnectionEntry[] {
  const trimmed = query.trim().toLowerCase();
  if (!trimmed) {
    return entries;
  }
  return entries.filter(entry => matchesSearch(entry, trimmed));
}

function matchesSearch(entry: ConnectionEntry, query: string): boolean {
  const haystack = [entry.kind, entry.clientAddr, entry.port, entry.upstream].join(' ').toLowerCase();
  return haystack.includes(query);
}

function filterSessions(entries: SessionHistoryEntry[], query: string): SessionHistoryEntry[] {
  const trimmed = query.trim().toLowerCase();
  if (!trimmed) {
    return [...entries];
  }
  return entries.filter(entry =>
    [entry.id, entry.kind, entry.clientAddr, entry.upstream].join(' ').toLowerCase().includes(trimmed)
  );
}

function defaultMetrics(tag: string, activeTag: string): UpstreamMetrics {
  return {
    rtt: 0,
    rttTcp: Number.NaN,
    rttUdp: Number.NaN,
    jitter: 0,
    loss: 0,
    lossRate: 0,
    retransRate: 0,
    score: 0,
    scoreTcp: 0,
    scoreUdp: 0,
    reachable: false,
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
  sortState: ConnectionSortState,
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

function compareByKey(a: ConnectionEntry, b: ConnectionEntry, key: ConnectionSortKey): number {
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
      return compareNumber(a.createdAt, b.createdAt);
    default:
      return 0;
  }
}

function compareSessionEntries(
  a: SessionHistoryEntry,
  b: SessionHistoryEntry,
  sortState: SessionSortState
): number {
  let primary = 0;
  switch (sortState.key) {
    case 'id':
      primary = a.id.localeCompare(b.id);
      break;
    case 'protocol':
      primary = a.kind.localeCompare(b.kind);
      break;
    case 'client': {
      const parsedA = parseClientAddress(a.clientAddr);
      const parsedB = parseClientAddress(b.clientAddr);
      primary = compareHosts(parsedA.host, parsedB.host);
      if (primary === 0) {
        primary = compareNumber(parsedA.port, parsedB.port);
      }
      break;
    }
    case 'upstream':
      primary = a.upstream.localeCompare(b.upstream);
      break;
    case 'start':
      primary = compareNumber(a.startTime, b.startTime);
      break;
    case 'end':
      primary = compareNumber(a.endTime, b.endTime);
      break;
    case 'up':
      primary = compareNumber(a.bytesUp, b.bytesUp);
      break;
    case 'down':
      primary = compareNumber(a.bytesDown, b.bytesDown);
      break;
    default:
      primary = 0;
  }
  if (primary !== 0) {
    return sortState.direction === 'asc' ? primary : -primary;
  }
  return compareNumber(b.endTime, a.endTime);
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

function formatTimestamp(ms: number): string {
  if (!Number.isFinite(ms)) {
    return '-';
  }
  return new Date(ms).toLocaleString();
}

function formatApproxTimestamp(ms: number, approximate: boolean): string {
  const prefix = approximate ? '\u2248 ' : '';
  return `${prefix}${formatTimestamp(ms)}`;
}

function formatYaml(value: unknown, indentLevel = 0): string {
  const indent = '  '.repeat(indentLevel);
  if (value === null || value === undefined) {
    return 'null';
  }
  if (typeof value === 'string') {
    return formatYamlString(value);
  }
  if (typeof value === 'number' || typeof value === 'boolean') {
    return String(value);
  }
  if (Array.isArray(value)) {
    if (value.length === 0) {
      return '[]';
    }
    return value
      .map(item => {
        if (isScalar(item)) {
          return `${indent}- ${formatYaml(item, indentLevel + 1)}`;
        }
        return `${indent}-\n${formatYaml(item, indentLevel + 1)}`;
      })
      .join('\n');
  }
  if (typeof value === 'object') {
    const entries = Object.entries(value as Record<string, unknown>);
    if (entries.length === 0) {
      return '{}';
    }
    return entries
      .map(([key, val]) => {
        const safeKey = formatYamlKey(key);
        if (isScalar(val)) {
          return `${indent}${safeKey}: ${formatYaml(val, indentLevel + 1)}`;
        }
        return `${indent}${safeKey}:\n${formatYaml(val, indentLevel + 1)}`;
      })
      .join('\n');
  }
  return formatYamlString(String(value));
}

function isScalar(value: unknown): boolean {
  return (
    value === null ||
    value === undefined ||
    typeof value === 'string' ||
    typeof value === 'number' ||
    typeof value === 'boolean'
  );
}

function formatYamlKey(value: string): string {
  if (/^[A-Za-z0-9_.-]+$/.test(value)) {
    return value;
  }
  return formatYamlString(value);
}

function formatYamlString(value: string): string {
  if (value === '') {
    return '""';
  }
  if (/^[A-Za-z0-9_.\/-]+$/.test(value)) {
    return value;
  }
  return JSON.stringify(value);
}
