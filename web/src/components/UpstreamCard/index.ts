import type { UpstreamSnapshot, UpstreamMetrics } from '../../types';
import { createEl, clearChildren } from '../../utils/dom';
import { formatBps, formatMs, formatPercent, formatScore } from '../../utils/format';

export interface BestFlags {
  bestRtt: boolean;
  bestLoss: boolean;
  bestScore: boolean;
}

export interface UpstreamCardHandle {
  element: HTMLElement;
  update: (metrics: UpstreamMetrics, flags: BestFlags) => void;
  onTestTCP?: () => void;
  onTestUDP?: () => void;
  onDetails?: () => void;
}

export function createUpstreamCard(upstream: UpstreamSnapshot): UpstreamCardHandle {
  const card = createEl('div', 'upstream-card');
  card.dataset.tag = upstream.tag;
  const header = createEl('div', 'upstream-header');
  const title = createEl('div');
  const tag = createEl('div', 'upstream-tag', upstream.tag);
  const metaText = upstream.host === upstream.active_ip
    ? upstream.host
    : `${upstream.host} -> ${upstream.active_ip || '-'}`;
  const meta = createEl('div', 'upstream-meta', metaText);
  const badges = createEl('div', 'upstream-badges');

  title.appendChild(tag);
  title.appendChild(meta);
  header.appendChild(title);
  header.appendChild(badges);

  const scoreTrack = createEl('div', 'score-track');
  const scoreFill = createEl('div', 'score-fill');
  scoreTrack.appendChild(scoreFill);

  const list = createEl('div', 'metric-list');
  const rows = {
    bwTcp: createMetricDualRow('TCP'),
    bwUdp: createMetricDualRow('UDP'),
    rtt: createMetricRow('RTT'),
    jitter: createMetricRow('Jitter'),
    retrans: createMetricRow('Retrans'),
    loss: createMetricRow('Loss'),
    score: createMetricRow('Score'),
    scoreTcp: createMetricRow('Score TCP'),
    scoreUdp: createMetricRow('Score UDP'),
    utilization: createMetricDualRow('Utilization'),
    reachable: createMetricRow('Reachable')
  };

  list.appendChild(rows.score.row);
  list.appendChild(rows.reachable.row);

  const actions = createEl('div', 'upstream-actions');
  const detailsBtn = createEl('button', 'btn-test', 'Details');
  const testTcpBtn = createEl('button', 'btn-test', 'Test TCP');
  const testUdpBtn = createEl('button', 'btn-test', 'Test UDP');
  actions.appendChild(detailsBtn);
  actions.appendChild(testTcpBtn);
  actions.appendChild(testUdpBtn);

  card.appendChild(header);
  card.appendChild(scoreTrack);
  card.appendChild(list);
  card.appendChild(actions);

  let onTestTCP: (() => void) | undefined;
  let onTestUDP: (() => void) | undefined;
  let onDetails: (() => void) | undefined;
  let testing = false;
  let pendingReset = false;

  const resetButtons = () => {
    testTcpBtn.disabled = false;
    testUdpBtn.disabled = false;
    testTcpBtn.textContent = 'Test TCP';
    testUdpBtn.textContent = 'Test UDP';
    testing = false;
    pendingReset = false;
  };

  const startTest = (protocol: 'tcp' | 'udp') => {
    if (testing) {
      return;
    }
    testing = true;
    pendingReset = true;
    testTcpBtn.disabled = true;
    testUdpBtn.disabled = true;
    if (protocol === 'tcp') {
      testTcpBtn.textContent = 'Testing...';
    } else {
      testUdpBtn.textContent = 'Testing...';
    }
  };

  detailsBtn.addEventListener('click', event => {
    event.stopPropagation();
    if (onDetails) {
      onDetails();
    }
  });

  testTcpBtn.addEventListener('click', event => {
    event.stopPropagation();
    if (!onTestTCP) {
      return;
    }
    startTest('tcp');
    onTestTCP();
  });

  testUdpBtn.addEventListener('click', event => {
    event.stopPropagation();
    if (!onTestUDP) {
      return;
    }
    startTest('udp');
    onTestUDP();
  });

  const update = (metrics: UpstreamMetrics, flags: BestFlags) => {
    rows.bwTcp.up.textContent = `${formatBps(metrics.bandwidthTcpUpBps)} \u2191`;
    rows.bwTcp.down.textContent = `${formatBps(metrics.bandwidthTcpDownBps)} \u2193`;
    rows.bwUdp.up.textContent = `${formatBps(metrics.bandwidthUdpUpBps)} \u2191`;
    rows.bwUdp.down.textContent = `${formatBps(metrics.bandwidthUdpDownBps)} \u2193`;
    rows.rtt.value.textContent = formatMs(metrics.rtt);
    rows.jitter.value.textContent = formatMs(metrics.jitter);
    rows.retrans.value.textContent = formatPercent(metrics.retransRate, 2);
    rows.loss.value.textContent = formatPercent(metrics.loss, 2);
    rows.score.value.textContent = formatScore(metrics.score);
    rows.scoreTcp.value.textContent = formatScore(metrics.scoreTcp);
    rows.scoreUdp.value.textContent = formatScore(metrics.scoreUdp);
    rows.utilization.up.textContent = `${formatPercent(metrics.utilizationUp, 1)} \u2191`;
    rows.utilization.down.textContent = `${formatPercent(metrics.utilizationDown, 1)} \u2193`;
    rows.reachable.value.textContent = metrics.reachable ? 'yes' : 'no';

    scoreFill.style.width = `${Math.max(0, Math.min(100, metrics.score))}%`;
    card.classList.toggle('active', metrics.active);
    card.classList.toggle('unusable', metrics.unusable);

    clearChildren(badges);
    if (metrics.active) {
      badges.appendChild(createBadge('active', 'active'));
    }
    if (metrics.unusable) {
      badges.appendChild(createBadge('unusable', 'unusable'));
    }
    if (flags.bestScore) {
      badges.appendChild(createBadge('top score', 'best'));
    }
    if (flags.bestRtt) {
      badges.appendChild(createBadge('best rtt', 'best'));
    }
    if (flags.bestLoss) {
      badges.appendChild(createBadge('best loss', 'best'));
    }

    if (pendingReset) {
      resetButtons();
    }
  };

  return {
    element: card,
    update,
    get onTestTCP() {
      return onTestTCP;
    },
    set onTestTCP(handler: (() => void) | undefined) {
      onTestTCP = handler;
    },
    get onTestUDP() {
      return onTestUDP;
    },
    set onTestUDP(handler: (() => void) | undefined) {
      onTestUDP = handler;
    },
    get onDetails() {
      return onDetails;
    },
    set onDetails(handler: (() => void) | undefined) {
      onDetails = handler;
    }
  };
}

export function getAllMetrics(metrics: UpstreamMetrics, upstream: UpstreamSnapshot) {
  return {
    tag: upstream.tag,
    host: upstream.host,
    activeIp: upstream.active_ip,
    bwTcp: { up: metrics.bandwidthTcpUpBps, down: metrics.bandwidthTcpDownBps },
    bwUdp: { up: metrics.bandwidthUdpUpBps, down: metrics.bandwidthUdpDownBps },
    rtt: metrics.rtt,
    jitter: metrics.jitter,
    retrans: metrics.retransRate,
    loss: metrics.loss,
    score: metrics.score,
    scoreTcp: metrics.scoreTcp,
    scoreUdp: metrics.scoreUdp,
    utilization: { up: metrics.utilizationUp, down: metrics.utilizationDown },
    reachable: metrics.reachable
  };
}

function createMetricRow(label: string) {
  const row = createEl('div', 'metric-row');
  const name = createEl('span');
  name.textContent = label;
  const value = createEl('strong');
  value.textContent = '-';
  row.appendChild(name);
  row.appendChild(value);
  return { row, value };
}

function createMetricDualRow(label: string) {
  const row = createEl('div', 'metric-row');
  const name = createEl('span');
  name.textContent = label;
  const value = createEl('strong', 'metric-dual');
  const up = createEl('span', 'metric-dual-item');
  const down = createEl('span', 'metric-dual-item');
  up.textContent = '-';
  down.textContent = '-';
  value.appendChild(up);
  value.appendChild(down);
  row.appendChild(name);
  row.appendChild(value);
  return { row, up, down };
}

function createBadge(text: string, variant: string) {
  return createEl('span', `badge ${variant}`, text);
}
