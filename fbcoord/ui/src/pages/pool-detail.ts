import type { NodeDetail, PoolDetail } from '../types.js';

function formatRelativeTime(timestamp: number): string {
  const deltaSeconds = Math.max(0, Math.floor((Date.now() - timestamp) / 1000));
  if (deltaSeconds < 60) {
    return `${deltaSeconds}s ago`;
  }
  if (deltaSeconds < 3600) {
    return `${Math.floor(deltaSeconds / 60)}m ago`;
  }
  if (deltaSeconds < 86400) {
    return `${Math.floor(deltaSeconds / 3600)}h ago`;
  }
  return `${Math.floor(deltaSeconds / 86400)}d ago`;
}

function formatDuration(fromTimestamp: number): string {
  const deltaSeconds = Math.max(0, Math.floor((Date.now() - fromTimestamp) / 1000));
  if (deltaSeconds < 60) {
    return `${deltaSeconds}s`;
  }
  if (deltaSeconds < 3600) {
    return `${Math.floor(deltaSeconds / 60)}m`;
  }
  if (deltaSeconds < 86400) {
    return `${Math.floor(deltaSeconds / 3600)}h`;
  }
  return `${Math.floor(deltaSeconds / 86400)}d`;
}

function renderUpstreams(node: NodeDetail, coordinatedPick: string | null): HTMLElement {
  const list = document.createElement('div');
  list.className = 'tag-list';
  for (const upstream of node.upstreams) {
    const tag = document.createElement('span');
    tag.className = `tag${coordinatedPick === upstream ? ' pick' : ''}`;
    tag.textContent = upstream;
    list.append(tag);
  }
  return list;
}

export function renderPoolDetailPage(detail: PoolDetail): HTMLElement {
  const shell = document.createElement('main');
  shell.className = 'shell';

  const toolbar = document.createElement('section');
  toolbar.className = 'toolbar';

  const nav = document.createElement('div');
  nav.className = 'nav-links';
  const dashboardLink = document.createElement('a');
  dashboardLink.className = 'pill';
  dashboardLink.href = '#/';
  dashboardLink.textContent = 'Back to Dashboard';
  const tokenLink = document.createElement('a');
  tokenLink.className = 'pill';
  tokenLink.href = '#/token';
  tokenLink.textContent = 'Token Management';
  nav.append(dashboardLink, tokenLink);

  const summary = document.createElement('div');
  summary.className = 'nav-links';
  const pick = document.createElement('span');
  pick.className = 'pill active';
  pick.textContent = detail.pick.upstream ? `Pick ${detail.pick.upstream}` : 'No consensus';
  const version = document.createElement('span');
  version.className = 'pill';
  version.textContent = `Version ${detail.pick.version}`;
  summary.append(pick, version);

  toolbar.append(nav, summary);

  const panel = document.createElement('section');
  panel.className = 'panel';

  const header = document.createElement('div');
  header.className = 'panel-header detail-header';

  const heading = document.createElement('div');
  const kicker = document.createElement('div');
  kicker.className = 'kicker';
  kicker.textContent = 'Pool';
  const title = document.createElement('h1');
  title.textContent = detail.pool;
  const count = document.createElement('p');
  count.className = 'muted';
  count.textContent = `${detail.node_count} connected node${detail.node_count === 1 ? '' : 's'}`;
  heading.append(kicker, title, count);

  const subtitle = document.createElement('div');
  subtitle.className = 'muted';
  subtitle.textContent = detail.pick.upstream
    ? 'Nodes with a different active upstream are currently falling back or still converging.'
    : 'No shared upstream is currently selected for this pool.';

  header.append(heading, subtitle);

  const body = document.createElement('div');
  body.className = 'panel-body table-wrap';

  const table = document.createElement('table');
  const thead = document.createElement('thead');
  thead.innerHTML = `
    <tr>
      <th>Node ID</th>
      <th>Submitted Upstreams</th>
      <th>Active Upstream</th>
      <th>Last Seen</th>
      <th>Connection Age</th>
    </tr>
  `;
  table.append(thead);

  const tbody = document.createElement('tbody');
  for (const node of detail.nodes) {
    const row = document.createElement('tr');

    const nodeId = document.createElement('td');
    nodeId.className = 'inline-code';
    nodeId.textContent = node.node_id;

    const upstreams = document.createElement('td');
    upstreams.append(renderUpstreams(node, detail.pick.upstream));

    const active = document.createElement('td');
    active.className = `inline-code${detail.pick.upstream && node.active_upstream !== detail.pick.upstream ? ' status-warn' : ''}`;
    active.textContent = node.active_upstream ?? 'none';

    const lastSeen = document.createElement('td');
    lastSeen.textContent = formatRelativeTime(node.last_seen);

    const connectedAt = document.createElement('td');
    connectedAt.textContent = formatDuration(node.connected_at);

    row.append(nodeId, upstreams, active, lastSeen, connectedAt);
    tbody.append(row);
  }
  table.append(tbody);
  body.append(table);
  panel.append(header, body);

  shell.append(toolbar, panel);
  return shell;
}
