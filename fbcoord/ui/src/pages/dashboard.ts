import type { PoolSummary } from '../types.js';

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

export function renderDashboardPage(options: {
  pools: PoolSummary[];
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
      <p>Active pools, coordinated picks, and the node counts behind each decision.</p>
    </div>
  `;

  const heroMeta = document.createElement('div');
  heroMeta.className = 'hero-meta';
  heroMeta.append(
    summaryCard('Active Pools', String(options.pools.length)),
    summaryCard(
      'Live Picks',
      String(options.pools.filter(pool => pool.pick.upstream !== null).length),
      'Pools with a shared upstream right now'
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

  const grid = document.createElement('section');
  grid.className = 'grid pool-grid';

  if (options.pools.length === 0) {
    const empty = document.createElement('div');
    empty.className = 'panel empty-state';
    empty.textContent = 'No active pools are currently registered.';
    grid.append(empty);
  } else {
    for (const pool of options.pools) {
      const link = document.createElement('a');
      link.className = 'panel pool-card';
      link.href = `#/pool/${encodeURIComponent(pool.name)}`;

      const body = document.createElement('div');
      body.className = 'panel-body stack';

      const header = document.createElement('div');
      header.className = 'kicker';
      header.textContent = pool.name;

      const nodes = document.createElement('div');
      nodes.className = 'value';
      nodes.textContent = `${pool.node_count} node${pool.node_count === 1 ? '' : 's'}`;

      const pick = document.createElement('div');
      pick.className = 'muted inline-code';
      pick.textContent = pool.pick.upstream ?? 'no consensus';

      const version = document.createElement('div');
      version.className = 'muted';
      version.textContent = `Version ${pool.pick.version}`;

      body.append(header, nodes, pick, version);
      link.append(body);
      grid.append(link);
    }
  }

  shell.append(hero, toolbar, grid);
  return shell;
}
