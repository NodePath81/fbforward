import type { CoordinationState, NodeDetail } from '../types.js';

function formatRelativeTime(timestamp: number | null): string {
  if (timestamp === null) {
    return 'Never';
  }
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

function summaryCard(label: string, value: string, extra?: string): HTMLElement {
  const card = document.createElement('div');
  card.className = 'panel pool-card';

  const body = document.createElement('div');
  body.className = 'panel-body stack';

  const kicker = document.createElement('div');
  kicker.className = 'kicker';
  kicker.textContent = label;

  const strong = document.createElement('div');
  strong.className = 'value';
  strong.textContent = value;

  body.append(kicker, strong);

  if (extra) {
    const tail = document.createElement('div');
    tail.className = 'muted';
    tail.textContent = extra;
    body.append(tail);
  }

  card.append(body);
  return card;
}

function renderUpstreams(node: NodeDetail, coordinatedPick: string | null): HTMLElement {
  const list = document.createElement('div');
  list.className = 'tag-list';
  if (node.upstreams.length === 0) {
    const empty = document.createElement('span');
    empty.className = 'muted';
    empty.textContent = 'none';
    list.append(empty);
    return list;
  }

  for (const upstream of node.upstreams) {
    const tag = document.createElement('span');
    tag.className = `tag${coordinatedPick === upstream ? ' pick' : ''}`;
    tag.textContent = upstream;
    list.append(tag);
  }
  return list;
}

function statusClass(status: NodeDetail['status']): string {
  if (status === 'online') {
    return 'status-good';
  }
  if (status === 'aborted') {
    return 'status-warn';
  }
  return 'muted';
}

function formatDisconnected(node: NodeDetail): string {
  if (node.status === 'online') {
    return 'Active';
  }
  return formatRelativeTime(node.disconnected_at);
}

function activeUpstreamText(node: NodeDetail): string {
  if (node.status !== 'online') {
    return 'none';
  }
  return node.active_upstream ?? 'none';
}

export function renderDashboardPage(options: {
  state: CoordinationState;
  pollIntervalMs: number;
  onPollIntervalChange: (ms: number) => void;
}): HTMLElement {
  const shell = document.createElement('main');
  shell.className = 'shell';

  const hero = document.createElement('section');
  hero.className = 'hero';
  hero.innerHTML = `
    <div>
      <div class="kicker">fbcoord</div>
      <h1>Coordination Dashboard</h1>
      <p>One global coordination state, one shared pick, and a persisted roster of node coordination presence.</p>
    </div>
  `;

  const heroMeta = document.createElement('div');
  heroMeta.className = 'hero-meta';
  heroMeta.append(
    summaryCard('Online', String(options.state.counts.online)),
    summaryCard('Offline', String(options.state.counts.offline)),
    summaryCard('Aborted', String(options.state.counts.aborted)),
    summaryCard('Never Seen', String(options.state.counts.never_seen)),
    summaryCard(
      'Current Pick',
      options.state.pick.upstream ?? 'no consensus',
      `Version ${options.state.pick.version}`
    )
  );
  hero.append(heroMeta);

  const toolbar = document.createElement('section');
  toolbar.className = 'toolbar';

  const nav = document.createElement('div');
  nav.className = 'nav-links';
  const tokenLink = document.createElement('a');
  tokenLink.className = 'pill';
  tokenLink.href = '#/token';
  tokenLink.textContent = 'Token Management';
  nav.append(tokenLink);

  const pollControls = document.createElement('div');
  pollControls.className = 'poll-controls';
  for (const option of [2000, 5000, 15000]) {
    const button = document.createElement('button');
    button.className = `poll-option${options.pollIntervalMs === option ? ' active' : ''}`;
    button.type = 'button';
    button.textContent = `${option / 1000}s poll`;
    button.addEventListener('click', () => options.onPollIntervalChange(option));
    pollControls.append(button);
  }

  toolbar.append(nav, pollControls);

  const panel = document.createElement('section');
  panel.className = 'panel';

  const header = document.createElement('div');
  header.className = 'panel-header detail-header';

  const heading = document.createElement('div');
  const kicker = document.createElement('div');
  kicker.className = 'kicker';
  kicker.textContent = 'Node Coordination Presence';
  const title = document.createElement('h2');
  title.textContent = options.state.pick.upstream ?? 'No consensus';
  const count = document.createElement('p');
  count.className = 'muted';
  count.textContent = `${options.state.node_count} online node${options.state.node_count === 1 ? '' : 's'}`;
  heading.append(kicker, title, count);

  const subtitle = document.createElement('div');
  subtitle.className = 'muted';
  subtitle.textContent = 'Only online nodes contribute to the coordinated pick. Offline, aborted, and never-seen nodes remain visible for operators.';

  header.append(heading, subtitle);

  const body = document.createElement('div');
  body.className = 'panel-body table-wrap';

  if (options.state.nodes.length === 0) {
    const empty = document.createElement('div');
    empty.className = 'empty-state';
    empty.textContent = 'No provisioned node tokens exist yet.';
    body.append(empty);
  } else {
    const table = document.createElement('table');
    const thead = document.createElement('thead');
    thead.innerHTML = `
      <tr>
        <th>Node ID</th>
        <th>Status</th>
        <th>Submitted Upstreams</th>
        <th>Active Upstream</th>
        <th>Last Seen</th>
        <th>Disconnected</th>
        <th>First Seen</th>
      </tr>
    `;
    table.append(thead);

    const tbody = document.createElement('tbody');
    for (const node of options.state.nodes) {
      const row = document.createElement('tr');

      const nodeId = document.createElement('td');
      nodeId.className = 'inline-code';
      nodeId.textContent = node.node_id;

      const status = document.createElement('td');
      status.className = statusClass(node.status);
      status.textContent = node.status;

      const upstreams = document.createElement('td');
      upstreams.append(renderUpstreams(node, options.state.pick.upstream));

      const active = document.createElement('td');
      active.className = `inline-code${node.status === 'online' && options.state.pick.upstream && node.active_upstream !== options.state.pick.upstream ? ' status-warn' : ''}`;
      active.textContent = activeUpstreamText(node);

      const lastSeen = document.createElement('td');
      lastSeen.textContent = formatRelativeTime(node.last_seen_at);

      const disconnected = document.createElement('td');
      disconnected.textContent = formatDisconnected(node);

      const firstSeen = document.createElement('td');
      firstSeen.textContent = formatRelativeTime(node.first_seen_at);

      row.append(nodeId, status, upstreams, active, lastSeen, disconnected, firstSeen);
      tbody.append(row);
    }
    table.append(tbody);
    body.append(table);
  }

  panel.append(header, body);
  shell.append(hero, toolbar, panel);
  return shell;
}
