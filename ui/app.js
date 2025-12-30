const storedToken = localStorage.getItem('fbforward_token') || '';
if (!storedToken) {
  window.location.href = '/auth';
} else {
  startApp(storedToken);
}

function startApp(token) {
  const modeSelect = document.getElementById('modeSelect');
  const upstreamSelect = document.getElementById('upstreamSelect');
  const applyButton = document.getElementById('applyButton');
  const restartButton = document.getElementById('restartButton');
  const authButton = document.getElementById('authButton');
  const controlHint = document.getElementById('controlHint');
  const upstreamGrid = document.getElementById('upstreamGrid');
  const modeLabel = document.getElementById('modeLabel');
  const activeLabel = document.getElementById('activeLabel');
  const tcpCount = document.getElementById('tcpCount');
  const udpCount = document.getElementById('udpCount');
  const tcpTable = document.getElementById('tcpTable');
  const udpTable = document.getElementById('udpTable');

  const state = {
    token: token,
    upstreams: {},
    upstreamEls: {},
    tcpMap: new Map(),
    udpMap: new Map(),
    ws: null
  };

  authButton.addEventListener('click', () => {
    window.location.href = '/auth';
  });

  applyButton.addEventListener('click', async () => {
    const mode = modeSelect.value;
    const tag = upstreamSelect.value;
    const params = { mode, tag };
    const result = await callRPC(state, 'SetUpstream', params);
    if (result.ok) {
      controlHint.textContent = 'Mode updated.';
      await loadStatus();
    } else {
      controlHint.textContent = result.error || 'Request failed.';
    }
  });

  restartButton.addEventListener('click', async () => {
    const result = await callRPC(state, 'Restart', {});
    if (result.ok) {
      controlHint.textContent = 'Restart requested.';
    } else {
      controlHint.textContent = result.error || 'Request failed.';
    }
  });

  async function loadStatus() {
    const resp = await callRPC(state, 'GetStatus', {});
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
      const card = createUpstreamCard(up);
      upstreamGrid.appendChild(card);
      state.upstreamEls[up.tag] = card;
    });
  }

  function createUpstreamCard(up) {
    const card = document.createElement('div');
    card.className = 'upstream-card';

    const tag = document.createElement('div');
    tag.className = 'upstream-tag';
    tag.textContent = up.tag;

    const meta = document.createElement('div');
    meta.className = 'upstream-meta';
    meta.textContent = up.host + ' -> ' + (up.active_ip || '-');

    card.appendChild(tag);
    card.appendChild(meta);
    card.appendChild(createMetricRow('RTT', 'rtt'));
    card.appendChild(createMetricRow('Jitter', 'jitter'));
    card.appendChild(createMetricRow('Loss', 'loss'));
    card.appendChild(createMetricRow('Score', 'score'));
    card.appendChild(createMetricRow('Status', 'status'));
    return card;
  }

  function createMetricRow(label, key) {
    const row = document.createElement('div');
    row.className = 'metric-row';
    const labelEl = document.createElement('span');
    labelEl.textContent = label;
    const valueEl = document.createElement('strong');
    valueEl.setAttribute('data-metric', key);
    valueEl.textContent = '-';
    row.appendChild(labelEl);
    row.appendChild(valueEl);
    return row;
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
          const parts = pair.split('=');
          const key = parts[0];
          const val = (parts[1] || '').replace(/"/g, '');
          labels[key] = val;
        });
      }
      if (!metrics[name]) metrics[name] = [];
      metrics[name].push({ labels, value });
    }
    return metrics;
  }

  async function pollMetrics() {
    try {
      const res = await fetch('/metrics', {
        headers: {
          'Authorization': 'Bearer ' + state.token
        }
      });
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
      row.appendChild(createCell(entry.id));
      row.appendChild(createCell(entry.client_addr));
      row.appendChild(createCell(entry.upstream));
      row.appendChild(createCell(formatBytes(entry.bytes_up)));
      row.appendChild(createCell(formatBytes(entry.bytes_down)));
      row.appendChild(createCell(new Date(entry.last_activity).toLocaleTimeString()));
      row.appendChild(createCell(entry.age + 's'));
      table.appendChild(row);
    });
  }

  function createCell(value) {
    const cell = document.createElement('td');
    cell.textContent = value;
    return cell;
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
    const protocols = [
      'fbforward',
      'fbforward-token.' + base64UrlEncode(state.token)
    ];
    const ws = new WebSocket(proto + '://' + location.host + '/status', protocols);
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

  function base64UrlEncode(text) {
    const encoder = new TextEncoder();
    const bytes = encoder.encode(text);
    let binary = '';
    bytes.forEach(value => {
      binary += String.fromCharCode(value);
    });
    return btoa(binary)
      .replace(/\+/g, '-')
      .replace(/\//g, '_')
      .replace(/=+$/g, '');
  }
}

async function callRPC(state, method, params) {
  try {
    const res = await fetch('/rpc', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': 'Bearer ' + state.token
      },
      body: JSON.stringify({ method, params })
    });
    return await res.json();
  } catch (err) {
    return { ok: false, error: 'network error' };
  }
}
