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
}

export function createUpstreamCard(upstream: UpstreamSnapshot): UpstreamCardHandle {
  const card = createEl('div', 'upstream-card');
  const header = createEl('div', 'upstream-header');
  const title = createEl('div');
  const tag = createEl('div', 'upstream-tag', upstream.tag);
  const meta = createEl('div', 'upstream-meta', `${upstream.host} -> ${upstream.active_ip || '-'}`);
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
    loss: createMetricRow('Loss'),
    score: createMetricRow('Score'),
    status: createMetricRow('Status')
  };

  list.appendChild(rows.rtt.row);
  list.appendChild(rows.jitter.row);
  list.appendChild(rows.loss.row);
  list.appendChild(rows.score.row);
  list.appendChild(rows.status.row);

  card.appendChild(header);
  card.appendChild(scoreTrack);
  card.appendChild(list);

  const update = (metrics: UpstreamMetrics, flags: BestFlags) => {
    rows.rtt.value.textContent = formatMs(metrics.rtt);
    rows.jitter.value.textContent = formatMs(metrics.jitter);
    rows.loss.value.textContent = formatPercent(metrics.loss, 1);
    rows.score.value.textContent = formatScore(metrics.score);
    rows.status.value.textContent = metrics.unusable ? 'unusable' : 'ok';

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

  return { element: card, update };
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
