package main

import (
	"net/http"
)

const webIndexHTML = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8"/>
    <meta name="viewport" content="width=device-width, initial-scale=1"/>
    <title>fbforward</title>
    <link rel="stylesheet" href="/styles.css"/>
  </head>
  <body>
    <div class="bg-orb orb-a"></div>
    <div class="bg-orb orb-b"></div>
    <main class="shell">
      <header class="hero">
        <div>
          <p class="eyebrow">Userspace NAT forwarder</p>
          <h1>fbforward</h1>
          <p class="subhead">Adaptive TCP/UDP forwarding driven by live ICMP quality scoring.</p>
        </div>
        <div class="status-card">
          <div class="status-row">
            <span>Mode</span>
            <strong id="modeLabel">auto</strong>
          </div>
          <div class="status-row">
            <span>Active upstream</span>
            <strong id="activeLabel">-</strong>
          </div>
          <div class="status-row">
            <span>TCP conns</span>
            <strong id="tcpCount">0</strong>
          </div>
          <div class="status-row">
            <span>UDP mappings</span>
            <strong id="udpCount">0</strong>
          </div>
        </div>
      </header>

      <section class="panel">
        <h2>Control</h2>
        <div class="control-grid">
          <div class="control-field">
            <label for="tokenInput">Token</label>
            <input id="tokenInput" type="password" placeholder="Paste control token"/>
          </div>
          <div class="control-field">
            <label for="modeSelect">Mode</label>
            <select id="modeSelect">
              <option value="auto">auto</option>
              <option value="manual">manual</option>
            </select>
          </div>
          <div class="control-field">
            <label for="upstreamSelect">Manual upstream</label>
            <select id="upstreamSelect"></select>
          </div>
          <div class="control-field buttons">
            <button id="applyButton">Apply</button>
            <button id="restartButton" class="outline">Restart</button>
          </div>
        </div>
        <p class="hint" id="controlHint"></p>
      </section>

      <section class="panel">
        <h2>Upstreams</h2>
        <div id="upstreamGrid" class="upstream-grid"></div>
      </section>

      <section class="panel">
        <h2>Active TCP connections</h2>
        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>ID</th>
                <th>Client</th>
                <th>Upstream</th>
                <th>Up</th>
                <th>Down</th>
                <th>Last activity</th>
                <th>Age</th>
              </tr>
            </thead>
            <tbody id="tcpTable"></tbody>
          </table>
        </div>
      </section>

      <section class="panel">
        <h2>Active UDP mappings</h2>
        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>ID</th>
                <th>Client</th>
                <th>Upstream</th>
                <th>Up</th>
                <th>Down</th>
                <th>Last activity</th>
                <th>Age</th>
              </tr>
            </thead>
            <tbody id="udpTable"></tbody>
          </table>
        </div>
      </section>
    </main>

    <script src="/app.js"></script>
  </body>
</html>
`

const webStylesCSS = `
:root {
  color-scheme: light;
  --bg: #f4f1ea;
  --bg-soft: #f9f7f2;
  --ink: #141414;
  --muted: #5c5c5c;
  --accent: #d96c4a;
  --accent-dark: #a35138;
  --card: #ffffff;
  --border: rgba(20, 20, 20, 0.08);
  --shadow: 0 20px 40px rgba(20, 20, 20, 0.12);
  --radius: 18px;
  --mono: "JetBrains Mono", "IBM Plex Mono", "Menlo", monospace;
  --sans: "Space Grotesk", "IBM Plex Sans", "Noto Sans", sans-serif;
}

* {
  box-sizing: border-box;
}

body {
  margin: 0;
  font-family: var(--sans);
  background: radial-gradient(circle at 20% 20%, rgba(217, 108, 74, 0.18), transparent 45%),
              radial-gradient(circle at 80% 0%, rgba(14, 142, 173, 0.2), transparent 40%),
              linear-gradient(180deg, #fdfcf9 0%, #efe8dc 100%);
  color: var(--ink);
  min-height: 100vh;
  position: relative;
  overflow-x: hidden;
}

.bg-orb {
  position: absolute;
  width: 420px;
  height: 420px;
  border-radius: 50%;
  filter: blur(60px);
  opacity: 0.35;
  z-index: 0;
}

.orb-a {
  background: #ffb199;
  top: -120px;
  left: -100px;
}

.orb-b {
  background: #98d7e6;
  bottom: -160px;
  right: -120px;
}

.shell {
  position: relative;
  z-index: 1;
  max-width: 1200px;
  margin: 0 auto;
  padding: 48px 24px 80px;
  display: flex;
  flex-direction: column;
  gap: 28px;
}

.hero {
  display: flex;
  flex-wrap: wrap;
  gap: 32px;
  align-items: center;
  justify-content: space-between;
}

.eyebrow {
  text-transform: uppercase;
  letter-spacing: 0.28em;
  font-size: 12px;
  color: var(--muted);
  margin: 0 0 8px;
}

h1 {
  margin: 0;
  font-size: clamp(36px, 5vw, 64px);
}

.subhead {
  margin: 12px 0 0;
  color: var(--muted);
  max-width: 520px;
}

.status-card {
  background: var(--card);
  border-radius: var(--radius);
  padding: 20px 24px;
  min-width: 240px;
  box-shadow: var(--shadow);
  border: 1px solid var(--border);
}

.status-row {
  display: flex;
  justify-content: space-between;
  font-size: 14px;
  margin: 8px 0;
  color: var(--muted);
}

.status-row strong {
  color: var(--ink);
}

.panel {
  background: var(--card);
  border-radius: var(--radius);
  padding: 24px;
  box-shadow: var(--shadow);
  border: 1px solid var(--border);
}

.panel h2 {
  margin-top: 0;
  font-size: 20px;
}

.control-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 16px;
  align-items: end;
}

.control-field {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.control-field label {
  font-size: 12px;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  color: var(--muted);
}

input, select {
  padding: 10px 12px;
  border-radius: 12px;
  border: 1px solid var(--border);
  font-family: var(--sans);
  font-size: 14px;
  background: var(--bg-soft);
}

button {
  padding: 12px 18px;
  border-radius: 12px;
  border: none;
  background: var(--accent);
  color: white;
  font-weight: 600;
  cursor: pointer;
  transition: transform 0.2s ease, box-shadow 0.2s ease;
}

button:hover {
  transform: translateY(-1px);
  box-shadow: 0 8px 18px rgba(217, 108, 74, 0.3);
}

button.outline {
  background: transparent;
  color: var(--accent-dark);
  border: 1px solid rgba(217, 108, 74, 0.4);
}

.buttons {
  display: flex;
  gap: 12px;
}

.hint {
  font-size: 12px;
  color: var(--muted);
  margin-top: 12px;
}

.upstream-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 16px;
}

.upstream-card {
  border-radius: 16px;
  padding: 16px;
  border: 1px solid var(--border);
  background: linear-gradient(135deg, #fffaf6, #f8f2e7);
  position: relative;
}

.upstream-card.active {
  border-color: rgba(217, 108, 74, 0.6);
  box-shadow: 0 12px 30px rgba(217, 108, 74, 0.18);
}

.upstream-tag {
  font-weight: 700;
  font-size: 18px;
  margin-bottom: 6px;
}

.upstream-meta {
  font-family: var(--mono);
  font-size: 12px;
  color: var(--muted);
  word-break: break-all;
}

.metric-row {
  display: flex;
  justify-content: space-between;
  font-size: 13px;
  margin-top: 8px;
  color: var(--muted);
}

.metric-row strong {
  color: var(--ink);
}

.table-wrap {
  overflow-x: auto;
}

table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}

th, td {
  text-align: left;
  padding: 10px 8px;
  border-bottom: 1px solid var(--border);
}

thead {
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--muted);
}

tbody tr:hover {
  background: rgba(217, 108, 74, 0.08);
}

@media (max-width: 720px) {
  .hero {
    flex-direction: column;
    align-items: flex-start;
  }
  .buttons {
    flex-direction: column;
  }
}
`

const webAppJS = `
const tokenInput = document.getElementById('tokenInput');
const modeSelect = document.getElementById('modeSelect');
const upstreamSelect = document.getElementById('upstreamSelect');
const applyButton = document.getElementById('applyButton');
const restartButton = document.getElementById('restartButton');
const controlHint = document.getElementById('controlHint');
const upstreamGrid = document.getElementById('upstreamGrid');
const modeLabel = document.getElementById('modeLabel');
const activeLabel = document.getElementById('activeLabel');
const tcpCount = document.getElementById('tcpCount');
const udpCount = document.getElementById('udpCount');
const tcpTable = document.getElementById('tcpTable');
const udpTable = document.getElementById('udpTable');

const state = {
  token: localStorage.getItem('fbforward_token') || '',
  upstreams: {},
  upstreamEls: {},
  tcpMap: new Map(),
  udpMap: new Map(),
  ws: null
};

tokenInput.value = state.token;

tokenInput.addEventListener('input', () => {
  state.token = tokenInput.value.trim();
  localStorage.setItem('fbforward_token', state.token);
  reconnectStatus();
});

applyButton.addEventListener('click', async () => {
  const mode = modeSelect.value;
  const tag = upstreamSelect.value;
  const params = { mode, tag };
  const result = await callRPC('SetUpstream', params);
  if (result.ok) {
    controlHint.textContent = 'Mode updated.';
    await loadStatus();
  } else {
    controlHint.textContent = result.error || 'Request failed.';
  }
});

restartButton.addEventListener('click', async () => {
  const result = await callRPC('Restart', {});
  if (result.ok) {
    controlHint.textContent = 'Restart requested.';
  } else {
    controlHint.textContent = result.error || 'Request failed.';
  }
});

function authHeaders() {
  if (!state.token) {
    return {};
  }
  return { 'Authorization': 'Bearer ' + state.token };
}

async function callRPC(method, params) {
  try {
    const res = await fetch('/rpc', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...authHeaders()
      },
      body: JSON.stringify({ method, params })
    });
    return await res.json();
  } catch (err) {
    return { ok: false, error: 'network error' };
  }
}

async function loadStatus() {
  const resp = await callRPC('GetStatus', {});
  if (!resp.ok) {
    controlHint.textContent = resp.error || 'Unable to load status.';
    return;
  }
  const data = resp.result;
  modeSelect.value = data.mode;
  modeLabel.textContent = data.mode;
  activeLabel.textContent = data.active_upstream || '-';
  tcpCount.textContent = data.counts.tcp_active;
  udpCount.textContent = data.counts.udp_active;

  upstreamGrid.innerHTML = '';
  upstreamSelect.innerHTML = '';
  state.upstreamEls = {};
  data.upstreams.forEach(up => {
    const option = document.createElement('option');
    option.value = up.tag;
    option.textContent = up.tag;
    upstreamSelect.appendChild(option);
    const card = document.createElement('div');
    card.className = 'upstream-card';
    card.innerHTML =
      '<div class="upstream-tag">' + up.tag + '</div>' +
      '<div class="upstream-meta">' + up.host + ' -> ' + (up.active_ip || '-') + '</div>' +
      '<div class="metric-row"><span>RTT</span><strong data-metric="rtt">-</strong></div>' +
      '<div class="metric-row"><span>Jitter</span><strong data-metric="jitter">-</strong></div>' +
      '<div class="metric-row"><span>Loss</span><strong data-metric="loss">-</strong></div>' +
      '<div class="metric-row"><span>Score</span><strong data-metric="score">-</strong></div>' +
      '<div class="metric-row"><span>Status</span><strong data-metric="status">-</strong></div>';
    upstreamGrid.appendChild(card);
    state.upstreamEls[up.tag] = card;
  });
}

function updateUpstreamCard(tag, metrics) {
  const card = state.upstreamEls[tag];
  if (!card) return;
  const set = (name, value) => {
    const el = card.querySelector('[data-metric="' + name + '"]');
    if (el) el.textContent = value;
  };
  set('rtt', metrics.rtt.toFixed(2) + ' ms');
  set('jitter', metrics.jitter.toFixed(2) + ' ms');
  set('loss', metrics.loss.toFixed(3));
  set('score', metrics.score.toFixed(1));
  set('status', metrics.unusable ? 'unusable' : 'ok');
  card.classList.toggle('active', metrics.active);
}

function parseMetrics(text) {
  const lines = text.split('\n');
  const metrics = {};
  for (const line of lines) {
    if (!line || line.startsWith('#')) continue;
    const match = line.match(/^([a-zA-Z0-9_:]+)(\{[^}]*\})?\s+([0-9eE\+\-\.]+)/);
    if (!match) continue;
    const name = match[1];
    const labelsRaw = match[2] || '';
    const value = parseFloat(match[3]);
    let labels = {};
    if (labelsRaw) {
      labelsRaw.slice(1, -1).split(',').forEach(pair => {
        const [k, v] = pair.split('=');
        labels[k] = v.replace(/"/g, '');
      });
    }
    if (!metrics[name]) metrics[name] = [];
    metrics[name].push({ labels, value });
  }
  return metrics;
}

async function pollMetrics() {
  try {
    const res = await fetch('/metrics');
    const text = await res.text();
    const data = parseMetrics(text);
    updateFromMetrics(data);
  } catch (err) {
  }
}

function updateFromMetrics(data) {
  const mode = data['fbforward_mode']?.[0]?.value ?? 0;
  modeLabel.textContent = mode === 1 ? 'manual' : 'auto';

  const tcp = data['fbforward_tcp_active']?.[0]?.value ?? 0;
  const udp = data['fbforward_udp_mappings_active']?.[0]?.value ?? 0;
  tcpCount.textContent = tcp.toString();
  udpCount.textContent = udp.toString();

  const active = new Set();
  (data['fbforward_active_upstream'] || []).forEach(item => {
    if (item.value === 1) {
      active.add(item.labels.upstream);
      activeLabel.textContent = item.labels.upstream;
    }
  });
  if (active.size === 0) {
    activeLabel.textContent = '-';
  }

  (data['fbforward_upstream_rtt_ms'] || []).forEach(item => {
    const tag = item.labels.upstream;
    const metrics = getMetricsSnapshot(tag);
    metrics.rtt = item.value;
    metrics.active = active.has(tag);
    setMetricsSnapshot(tag, metrics);
  });
  (data['fbforward_upstream_jitter_ms'] || []).forEach(item => {
    const tag = item.labels.upstream;
    const metrics = getMetricsSnapshot(tag);
    metrics.jitter = item.value;
    setMetricsSnapshot(tag, metrics);
  });
  (data['fbforward_upstream_loss'] || []).forEach(item => {
    const tag = item.labels.upstream;
    const metrics = getMetricsSnapshot(tag);
    metrics.loss = item.value;
    setMetricsSnapshot(tag, metrics);
  });
  (data['fbforward_upstream_score'] || []).forEach(item => {
    const tag = item.labels.upstream;
    const metrics = getMetricsSnapshot(tag);
    metrics.score = item.value;
    setMetricsSnapshot(tag, metrics);
  });
  (data['fbforward_upstream_unusable'] || []).forEach(item => {
    const tag = item.labels.upstream;
    const metrics = getMetricsSnapshot(tag);
    metrics.unusable = item.value === 1;
    setMetricsSnapshot(tag, metrics);
  });

  Object.keys(state.upstreamEls).forEach(tag => {
    updateUpstreamCard(tag, getMetricsSnapshot(tag));
  });
}

function getMetricsSnapshot(tag) {
  if (!state.upstreams[tag]) {
    state.upstreams[tag] = { rtt: 0, jitter: 0, loss: 0, score: 0, unusable: true, active: false };
  }
  return state.upstreams[tag];
}

function setMetricsSnapshot(tag, snapshot) {
  state.upstreams[tag] = snapshot;
}

function formatBytes(value) {
  const units = ['B', 'KB', 'MB', 'GB'];
  let idx = 0;
  let val = value;
  while (val > 1024 && idx < units.length - 1) {
    val /= 1024;
    idx++;
  }
  return val.toFixed(1) + ' ' + units[idx];
}

function renderTable(map, table) {
  table.innerHTML = '';
  map.forEach(entry => {
    const row = document.createElement('tr');
    row.innerHTML =
      '<td>' + entry.id + '</td>' +
      '<td>' + entry.client_addr + '</td>' +
      '<td>' + entry.upstream + '</td>' +
      '<td>' + formatBytes(entry.bytes_up) + '</td>' +
      '<td>' + formatBytes(entry.bytes_down) + '</td>' +
      '<td>' + new Date(entry.last_activity).toLocaleTimeString() + '</td>' +
      '<td>' + entry.age + 's</td>';
    table.appendChild(row);
  });
}

function handleStatusMessage(msg) {
  if (msg.type === 'snapshot') {
    state.tcpMap = new Map(msg.tcp.map(item => [item.id, item]));
    state.udpMap = new Map(msg.udp.map(item => [item.id, item]));
  } else if (msg.type === 'add' || msg.type === 'update') {
    const entry = msg.entry;
    if (entry.kind === 'tcp') {
      state.tcpMap.set(entry.id, entry);
    } else {
      state.udpMap.set(entry.id, entry);
    }
  } else if (msg.type === 'remove') {
    if (msg.kind === 'tcp') {
      state.tcpMap.delete(msg.id);
    } else {
      state.udpMap.delete(msg.id);
    }
  }
  renderTable(state.tcpMap, tcpTable);
  renderTable(state.udpMap, udpTable);
}

function reconnectStatus() {
  if (state.ws) {
    state.ws.close();
  }
  if (!state.token) {
    return;
  }
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const ws = new WebSocket(proto + '://' + location.host + '/status?token=' + encodeURIComponent(state.token));
  state.ws = ws;
  ws.addEventListener('open', () => {
    ws.send(JSON.stringify({ type: 'snapshot' }));
  });
  ws.addEventListener('message', event => {
    try {
      const msg = JSON.parse(event.data);
      handleStatusMessage(msg);
    } catch (err) {}
  });
}

loadStatus();
pollMetrics();
setInterval(pollMetrics, 1000);
reconnectStatus();
`

func WebUIHandler(enabled bool) http.Handler {
	if !enabled {
		return http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/index.html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(webIndexHTML))
		case "/styles.css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
			_, _ = w.Write([]byte(webStylesCSS))
		case "/app.js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = w.Write([]byte(webAppJS))
		default:
			http.NotFound(w, r)
		}
	})
}
