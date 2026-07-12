import type { UpstreamSnapshot, UpstreamMetrics } from '../../types';
import { createEl, clearChildren } from '../../utils/dom';
import { formatMs } from '../../utils/format';

export interface BestFlags {
  bestRtt: boolean;
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

  const list = createEl('div', 'metric-list');
  const rows = {
    rtt: createMetricRow('RTT'),
    health: createMetricRow('Health'),
    reachable: createMetricRow('Reachable')
  };

  list.appendChild(rows.health.row);
  list.appendChild(rows.rtt.row);
  list.appendChild(rows.reachable.row);

  const actions = createEl('div', 'upstream-actions');
  const detailsBtn = createEl('button', 'btn-test', 'Details');
  actions.appendChild(detailsBtn);

  card.appendChild(header);
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
    rows.health.value.textContent = metrics.healthState;
    rows.reachable.value.textContent = metrics.reachable ? 'yes' : 'no';

    card.classList.toggle('active', metrics.active);
    card.classList.toggle('unusable', metrics.unusable);

    clearChildren(badges);
    if (metrics.active) {
      badges.appendChild(createBadge('active', 'active'));
    }
    if (metrics.unusable) {
      badges.appendChild(createBadge('unusable', 'unusable'));
    }
    if (flags.bestRtt) {
      badges.appendChild(createBadge('best rtt', 'best'));
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
    health: metrics.healthState,
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
