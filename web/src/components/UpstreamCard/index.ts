import type { UpstreamSnapshot, UpstreamMetrics } from '../../types';
import { createEl, clearChildren } from '../../utils/dom';
import { formatMs, formatPercent, formatScore } from '../../utils/format';

export interface BestFlags {
  bestRtt: boolean;
  bestLoss: boolean;
  bestScore: boolean;
}

export interface UpstreamCardHandle {
  element: HTMLElement;
  update: (metrics: UpstreamMetrics, flags: BestFlags) => void;
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
    rtt: createMetricRow('RTT'),
    jitter: createMetricRow('Jitter'),
    retrans: createMetricRow('Retrans'),
    loss: createMetricRow('Loss'),
    score: createMetricRow('Score'),
    scoreTcp: createMetricRow('Score TCP'),
    scoreUdp: createMetricRow('Score UDP'),
    reachable: createMetricRow('Reachable')
  };

  list.appendChild(rows.score.row);
  list.appendChild(rows.reachable.row);

  const actions = createEl('div', 'upstream-actions');
  const detailsBtn = createEl('button', 'btn-test', 'Details');
  actions.appendChild(detailsBtn);

  card.appendChild(header);
  card.appendChild(scoreTrack);
  card.appendChild(list);
  card.appendChild(actions);

  let onDetails: (() => void) | undefined;

  detailsBtn.addEventListener('click', event => {
    event.stopPropagation();
    if (onDetails) {
      onDetails();
    }
  });

  const update = (metrics: UpstreamMetrics, flags: BestFlags) => {
    rows.rtt.value.textContent = formatMs(metrics.rtt);
    rows.jitter.value.textContent = formatMs(metrics.jitter);
    rows.retrans.value.textContent = formatPercent(metrics.retransRate, 2);
    rows.loss.value.textContent = formatPercent(metrics.loss, 2);
    rows.score.value.textContent = formatScore(metrics.score);
    rows.scoreTcp.value.textContent = formatScore(metrics.scoreTcp);
    rows.scoreUdp.value.textContent = formatScore(metrics.scoreUdp);
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
  };

  return {
    element: card,
    update,
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
    rtt: metrics.rtt,
    jitter: metrics.jitter,
    retrans: metrics.retransRate,
    loss: metrics.loss,
    score: metrics.score,
    scoreTcp: metrics.scoreTcp,
    scoreUdp: metrics.scoreUdp,
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

function createBadge(text: string, variant: string) {
  return createEl('span', `badge ${variant}`, text);
}
