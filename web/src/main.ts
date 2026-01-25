import { callRPC, getQueueStatus, getRuntimeConfig, runMeasurement } from './api/rpc';
import { fetchJSON, fetchText } from './api/client';
import { extractMetrics, parseMetrics } from './api/metrics';
import { createConnectionTable } from './components/ConnectionTable';
import { createQueueWidget } from './components/QueueWidget';
import { createUpstreamCard } from './components/UpstreamCard';
import { renderStatusCard } from './components/StatusCard';
import { createToastManager } from './components/Toast';
import { connectStatusSocket } from './websocket/status';
import { createInitialState, Store } from './state/store';
import { historyStore } from './state/history';
import type {
  ConnectionEntry,
  Mode,
  RawConnectionEntry,
  StatusResponse,
  TestHistoryEntry,
  UpstreamMetrics,
  IdentityResponse,
  WSMessage
} from './types';
import { clearChildren, createEl, qs } from './utils/dom';
import { formatBps, formatBytes, formatDuration, formatMs, formatPercent, formatScore } from './utils/format';

const storedToken = localStorage.getItem('fbforward_token') || '';
if (!storedToken) {
  window.location.href = '/auth';
} else {
  startApp(storedToken);
}

function startApp(token: string) {
  const store = new Store(createInitialState(token));

  const statusCard = renderStatusCard(qs<HTMLElement>(document, '#statusCard'));
  const queueWidget = createQueueWidget(qs<HTMLElement>(document, '#queueWidget'));
  const upstreamGrid = qs<HTMLElement>(document, '#upstreamGrid');
  const upstreamSummary = qs<HTMLElement>(document, '#upstreamSummary');
  const connectionsSummary = qs<HTMLElement>(document, '#connectionsSummary');
  const connectionTable = createConnectionTable(qs<HTMLElement>(document, '#connectionTable'));
  const connectionSearch = qs<HTMLInputElement>(document, '#connectionSearch');
  const toast = createToastManager(qs<HTMLElement>(document, '#toastRegion'));
  const restartButton = qs<HTMLButtonElement>(document, '#restartButton');
  const pollStatus = qs<HTMLElement>(document, '#pollStatus');
  const sortButtons = Array.from(
    document.querySelectorAll<HTMLButtonElement>('.sort-button')
  );
  const modeButtons = Array.from(
    document.querySelectorAll<HTMLButtonElement>('.segmented-button[data-mode]')
  );
  const intervalButtons = Array.from(
    document.querySelectorAll<HTMLButtonElement>('.polling-button')
  );
  const navLinks = Array.from(document.querySelectorAll<HTMLAnchorElement>('.page-nav-link'));
  const pages = Array.from(document.querySelectorAll<HTMLElement>('.page'));
  const testHistoryTable = qs<HTMLTableSectionElement>(document, '#testHistoryTable');
  const sessionHistoryTable = qs<HTMLTableSectionElement>(document, '#sessionHistoryTable');
  const configTree = qs<HTMLElement>(document, '#configTree');
  const testDetailsModal = qs<HTMLElement>(document, '#testDetailsModal');
  const upstreamDetailsModal = qs<HTMLElement>(document, '#upstreamDetailsModal');
  let pollTimer: number | null = null;
  let pollInProgress = false;
  let statusSocket: ReturnType<typeof connectStatusSocket> | null = null;
  let currentPage: 'dashboard' | 'history' | 'config' = 'dashboard';
  let openUpstreamTag: string | null = null;

  function getDefaultPollInterval(): number {
    const stored = localStorage.getItem('fbforward_poll_interval_ms');
    if (stored) {
      const parsed = Number.parseInt(stored, 10);
      if (Number.isFinite(parsed) && parsed > 0) {
        return parsed;
      }
    }
    for (const button of intervalButtons) {
      if (button.getAttribute('aria-pressed') === 'true') {
        const value = Number.parseInt(button.dataset.interval || '', 10);
        if (Number.isFinite(value) && value > 0) {
          return value * 1000;
        }
      }
    }
    return 1000;
  }

  let pollIntervalMs = getDefaultPollInterval();

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
    localStorage.setItem('fbforward_poll_interval_ms', pollIntervalMs.toString());
    updatePollIntervalUI();
    startPolling();
    statusSocket?.updateSnapshotInterval(pollIntervalMs);
  }

  function updatePollIntervalUI(): void {
    for (const button of intervalButtons) {
      const value = Number.parseInt(button.dataset.interval || '', 10);
      const active = value * 1000 === pollIntervalMs;
      button.classList.toggle('active', active);
      button.setAttribute('aria-pressed', active ? 'true' : 'false');
    }
  }

  function resolvePageFromHash(): 'dashboard' | 'history' | 'config' {
    const hash = window.location.hash.replace(/^#\/?/, '');
    if (hash === 'history') {
      return 'history';
    }
    if (hash === 'config') {
      return 'config';
    }
    return 'dashboard';
  }

  function setActivePage(page: 'dashboard' | 'history' | 'config'): void {
    currentPage = page;
    for (const el of pages) {
      const isActive = el.id === `page-${page}`;
      el.classList.toggle('hidden', !isActive);
    }
    for (const link of navLinks) {
      link.classList.toggle('active', link.dataset.page === page);
    }
    if (page === 'history') {
      renderTestHistory();
      renderSessionHistory();
    }
    if (page === 'config') {
      void loadRuntimeConfig();
    }
  }

  window.addEventListener('hashchange', () => {
    setActivePage(resolvePageFromHash());
  });

  document.addEventListener('keydown', event => {
    if (event.key === 'Escape') {
      if (!testDetailsModal.classList.contains('hidden')) {
        hideTestDetails();
      } else if (!upstreamDetailsModal.classList.contains('hidden')) {
        hideUpstreamDetails();
      }
    }
  });

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

  connectionSearch.addEventListener('input', () => {
    updateTables();
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
      card.onTestTCP = async () => {
        const result = await runMeasurement(token, upstream.tag, 'tcp');
        if (!result.ok) {
          toast.show(result.error || 'TCP test failed.', 'error');
        }
      };
      card.onTestUDP = async () => {
        const result = await runMeasurement(token, upstream.tag, 'udp');
        if (!result.ok) {
          toast.show(result.error || 'UDP test failed.', 'error');
        }
      };
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
    const total = entries.length;
    for (const entry of entries) {
      if (!entryOrder.has(entry.id)) {
        entryOrder.set(entry.id, entrySeq);
        entrySeq += 1;
      }
    }
    const filtered = filterEntries(entries, connectionSearch.value);
    filtered.sort((a, b) => compareEntries(a, b, sortState, entryOrder));
    connectionTable(filtered);
    connectionsSummary.textContent = `${total} active`;
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
        hostIPs: resp.result.ips || [],
        version: resp.result.version || ''
      });
      updateHeaderIdentity();
      updateStatusCard();
    } catch (err) {
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

  function renderTestHistory(): void {
    const tests = historyStore.getTests();
    clearChildren(testHistoryTable);
    if (tests.length === 0) {
      appendEmptyRow(testHistoryTable, 6, 'No test history yet');
      return;
    }
    for (const entry of tests) {
      const row = createEl('tr');
      row.classList.add('test-row-clickable');
      row.appendChild(createCell(formatTimestamp(entry.timestamp)));
      row.appendChild(createCell(entry.upstream));
      row.appendChild(createCell(entry.protocol.toUpperCase()));
      row.appendChild(createCell(entry.direction));
      row.appendChild(createCell(formatDuration(entry.durationMs / 1000)));
      row.appendChild(createCell(entry.success ? 'success' : 'failed'));
      row.addEventListener('click', () => {
        showTestDetails(entry);
      });
      testHistoryTable.appendChild(row);
    }
  }

  function renderSessionHistory(): void {
    const sessions = historyStore.getSessions();
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
      row.appendChild(createCell(formatTimestamp(entry.startTime)));
      row.appendChild(createCell(formatTimestamp(entry.endTime)));
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

  function showTestDetails(entry: TestHistoryEntry): void {
    clearChildren(testDetailsModal);
    const card = createEl('div', 'modal-card');
    const header = createEl('div', 'modal-header');
    const title = createEl('h3', 'modal-title', 'Test details');
    const closeButton = createEl('button', 'modal-close', 'Close') as HTMLButtonElement;
    header.appendChild(title);
    header.appendChild(closeButton);
    card.appendChild(header);

    const meta = createEl('div', 'modal-meta');
    meta.appendChild(createDetailRow('Timestamp', formatTimestamp(entry.timestamp)));
    meta.appendChild(createDetailRow('Upstream', entry.upstream));
    meta.appendChild(createDetailRow('Protocol', entry.protocol.toUpperCase()));
    meta.appendChild(createDetailRow('Direction', entry.direction));
    meta.appendChild(createDetailRow('Duration', formatDuration(entry.durationMs / 1000)));
    card.appendChild(meta);

    const metrics = createEl('div', 'modal-metrics');
    const bandwidth =
      entry.direction === 'upload' ? entry.bandwidthUpBps : entry.bandwidthDownBps;
    metrics.appendChild(createDetailRow('Bandwidth', formatBps(bandwidth ?? Number.NaN)));
    metrics.appendChild(createDetailRow('RTT', formatMs(entry.rttMs ?? Number.NaN)));
    metrics.appendChild(createDetailRow('Jitter', formatMs(entry.jitterMs ?? Number.NaN)));
    if (entry.protocol === 'udp') {
      metrics.appendChild(
        createDetailRow('Loss rate', formatPercent(entry.lossRate ?? Number.NaN, 2))
      );
    } else {
      metrics.appendChild(
        createDetailRow('Retrans rate', formatPercent(entry.retransRate ?? Number.NaN, 2))
      );
    }
    card.appendChild(metrics);

    if (!entry.success) {
      const errorBox = createEl('div', 'modal-error');
      errorBox.textContent = entry.error || 'Test failed';
      card.appendChild(errorBox);
    }

    closeButton.addEventListener('click', hideTestDetails);
    testDetailsModal.onclick = event => {
      if (event.target === testDetailsModal) {
        hideTestDetails();
      }
    };

    testDetailsModal.appendChild(card);
    testDetailsModal.classList.remove('hidden');
  }

  function hideTestDetails(): void {
    testDetailsModal.classList.add('hidden');
    clearChildren(testDetailsModal);
  }

  function renderUpstreamDetailsModal(tag: string): void {
    const state = store.getState();
    const upstream = state.upstreams.find(u => u.tag === tag);
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
    metrics.appendChild(
      createDetailRow(
        'TCP Bandwidth',
        `${formatBps(liveMetrics.bandwidthTcpUpBps)} ↑ / ${formatBps(liveMetrics.bandwidthTcpDownBps)} ↓`
      )
    );
    metrics.appendChild(
      createDetailRow(
        'UDP Bandwidth',
        `${formatBps(liveMetrics.bandwidthUdpUpBps)} ↑ / ${formatBps(liveMetrics.bandwidthUdpDownBps)} ↓`
      )
    );
    metrics.appendChild(createDetailRow('RTT', formatMs(liveMetrics.rtt)));
    metrics.appendChild(createDetailRow('Jitter', formatMs(liveMetrics.jitter)));
    metrics.appendChild(createDetailRow('Retrans rate', formatPercent(liveMetrics.retransRate, 2)));
    metrics.appendChild(createDetailRow('Loss rate', formatPercent(liveMetrics.lossRate, 2)));
    metrics.appendChild(
      createDetailRow(
        'Utilization',
        `${formatPercent(liveMetrics.utilizationUp, 1)} ↑ / ${formatPercent(liveMetrics.utilizationDown, 1)} ↓`
      )
    );
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
    const upstream = state.upstreams.find(u => u.tag === tag);
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
    if (state.hostIPs.length === 0) {
      ipEl.textContent = '-';
    } else {
      ipEl.textContent = state.hostIPs.join(', ');
    }
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
    if (state.pollErrors.metrics || state.pollErrors.queue) {
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
      const pollErrors = store.getState().pollErrors;
      store.setState({
        pollErrors: { ...pollErrors, metrics: null }
      });
      updateStatusCard();
      updateUpstreamCards();
    } catch (err) {
      const pollErrors = store.getState().pollErrors;
      store.setState({
        pollErrors: {
          ...pollErrors,
          metrics: err instanceof Error ? err.message : 'Failed to fetch metrics'
        }
      });
      updatePollStatus();
    }
  }

  async function pollQueue(): Promise<void> {
    try {
      const timeoutMs = Math.max(1000, pollIntervalMs - 1000);
      const resp = await getQueueStatus(token, timeoutMs);
      if (resp.ok && resp.result) {
        queueWidget(resp.result);
        const pollErrors = store.getState().pollErrors;
        store.setState({
          pollErrors: { ...pollErrors, queue: null }
        });
      } else {
        queueWidget(null);
        const pollErrors = store.getState().pollErrors;
        store.setState({
          pollErrors: {
            ...pollErrors,
            queue: resp.error || 'Failed to fetch queue status'
          }
        });
      }
      updatePollStatus();
    } catch (err) {
      queueWidget(null);
      const pollErrors = store.getState().pollErrors;
      store.setState({
        pollErrors: {
          ...pollErrors,
          queue: err instanceof Error ? err.message : 'Failed to fetch queue status'
        }
      });
      updatePollStatus();
    }
  }

  function trackSessionEntry(entry: RawConnectionEntry): void {
    const startTime = Date.now() - entry.age * 1000;
    historyStore.trackSessionStart(entry.id, entry.kind, entry.client_addr, entry.upstream, startTime);
    historyStore.trackSessionUpdate(
      entry.id,
      entry.bytes_up,
      entry.bytes_down,
      entry.segments_up ?? 0,
      entry.segments_down ?? 0
    );
  }

  function handleStatusMessage(message: WSMessage): void {
    if (message.type === 'test_complete') {
      if (message.test_complete) {
        historyStore.addTest({
          timestamp: message.test_complete.timestamp,
          upstream: message.test_complete.upstream,
          protocol: message.test_complete.protocol,
          direction: message.test_complete.direction,
          durationMs: message.test_complete.duration_ms,
          success: message.test_complete.success,
          bandwidthUpBps: message.test_complete.bandwidth_up_bps,
          bandwidthDownBps: message.test_complete.bandwidth_down_bps,
          rttMs: message.test_complete.rtt_ms,
          jitterMs: message.test_complete.jitter_ms,
          lossRate: message.test_complete.loss_rate,
          retransRate: message.test_complete.retrans_rate,
          error: message.test_complete.error
        });
        if (currentPage === 'history') {
          renderTestHistory();
        }
      }
      return;
    }
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
      for (const entry of message.tcp || []) {
        trackSessionEntry(entry);
      }
      for (const entry of message.udp || []) {
        trackSessionEntry(entry);
      }
      if (currentPage === 'history') {
        renderSessionHistory();
      }
    }
    if (message.type === 'add' || message.type === 'update') {
      if (message.entry) {
        trackSessionEntry(message.entry);
      }
    }
    if (message.type === 'remove' && message.id) {
      historyStore.trackSessionEnd(message.id);
      if (currentPage === 'history') {
        renderSessionHistory();
      }
    }
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

  statusSocket = connectStatusSocket(
    {
      token,
      onMessage: handleStatusMessage
    },
    pollIntervalMs
  );

  loadStatus();
  loadIdentity();
  updatePollIntervalUI();
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
      await Promise.allSettled([pollMetrics(), pollQueue()]);
    } finally {
      pollInProgress = false;
    }
  }

  void executePollCycle();
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
    port: raw.port,
    upstream: raw.upstream,
    bytesUp: raw.bytes_up,
    bytesDown: raw.bytes_down,
    segmentsUp: raw.segments_up ?? 0,
    segmentsDown: raw.segments_down ?? 0,
    lastActivity: raw.last_activity,
    age: raw.age,
    kind: raw.kind
  };
}

function filterEntries(entries: ConnectionEntry[], query: string): ConnectionEntry[] {
  const trimmed = query.trim().toLowerCase();
  if (!trimmed) {
    return entries;
  }
  return entries.filter(entry => matchesSearch(entry, trimmed));
}

function matchesSearch(entry: ConnectionEntry, query: string): boolean {
  const haystack = [entry.kind, entry.clientAddr, entry.port, entry.upstream]
    .join(' ')
    .toLowerCase();
  return haystack.includes(query);
}

function defaultMetrics(tag: string, activeTag: string): UpstreamMetrics {
  return {
    rtt: 0,
    jitter: 0,
    loss: 0,
    lossRate: 0,
    retransRate: 0,
    score: 0,
    scoreTcp: 0,
    scoreUdp: 0,
    scoreOverall: 0,
    bandwidthUpBps: 0,
    bandwidthDownBps: 0,
    bandwidthTcpUpBps: 0,
    bandwidthTcpDownBps: 0,
    bandwidthUdpUpBps: 0,
    bandwidthUdpDownBps: 0,
    utilization: 0,
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

function formatTimestamp(ms: number): string {
  if (!Number.isFinite(ms)) {
    return '-';
  }
  return new Date(ms).toLocaleString();
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
    return '\"\"';
  }
  if (/^[A-Za-z0-9_.\/-]+$/.test(value)) {
    return value;
  }
  return JSON.stringify(value);
}
