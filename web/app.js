const tokenKey = 'fbforward.control_token';
const login = document.querySelector('#login');
const app = document.querySelector('#app');
const alertBox = document.querySelector('#alert');
const tokenInput = document.querySelector('#token');
const state = { page: 'status', timer: 0, inFlight: false, status: null };

function showAlert(message) { alertBox.textContent = message; alertBox.hidden = !message; }
function setAuthenticated(value) { login.hidden = value; app.hidden = !value; if (!value) stopPolling(); }
function logout() { sessionStorage.removeItem(tokenKey); setAuthenticated(false); tokenInput.value = ''; showAlert(''); }
function text(value) { return value == null ? '' : String(value); }
function cell(row, value) { const node = document.createElement('td'); node.textContent = text(value); row.append(node); }

async function rpc(method, params) {
  const headers = { Authorization: `Bearer ${sessionStorage.getItem(tokenKey) || ''}`, 'Content-Type': 'application/json' };
  const response = await fetch('/rpc', { method: 'POST', headers, body: JSON.stringify({ method, ...(params == null ? {} : { params }) }) });
  let payload = null;
  try { payload = await response.json(); } catch (_) { throw new Error(`HTTP ${response.status}`); }
  if (response.status === 401) { logout(); throw new Error('unauthorized'); }
  if (!response.ok || !payload.ok) throw new Error(payload.error || `HTTP ${response.status}`);
  return payload.result;
}

function renderStatus(data) {
  state.status = data;
  const summary = document.querySelector('#status-summary'); summary.replaceChildren();
  for (const [label, value] of [['mode', data.mode], ['active', data.active_upstream || 'none']]) { const item = document.createElement('span'); item.textContent = `${label}: ${text(value)}`; summary.append(item); }
  const mode = document.querySelector('#mode'); mode.value = data.mode === 'manual' ? 'manual' : 'auto';
  const select = document.querySelector('#manual-upstream'); select.replaceChildren();
  for (const up of data.upstreams || []) { const option = document.createElement('option'); option.value = up.tag; option.textContent = up.tag; option.selected = up.tag === data.active_upstream; select.append(option); }
  const rows = document.querySelector('#upstream-rows'); rows.replaceChildren();
  for (const up of data.upstreams || []) { const row = document.createElement('tr'); cell(row, up.tag); cell(row, up.health_state); cell(row, up.rtt_ms); cell(row, up.active ? 'yes' : 'no'); rows.append(row); }
}

function renderFlows(data) {
  const filter = document.querySelector('#flow-filter').value.toLowerCase();
  const rows = document.querySelector('#flow-rows'); rows.replaceChildren();
  for (const flow of [...(data.tcp || []), ...(data.udp || [])]) {
    const haystack = [flow.id, flow.client_addr, flow.listener, flow.route, flow.upstream].join(' ').toLowerCase();
    if (filter && !haystack.includes(filter)) continue;
    const row = document.createElement('tr'); cell(row, flow.id); cell(row, flow.client_addr); cell(row, flow.listener); cell(row, flow.route); cell(row, flow.upstream); cell(row, `${flow.bytes_up} / ${flow.bytes_down}`); cell(row, flow.last_activity); rows.append(row);
  }
}

async function refreshPage() {
  if (state.inFlight || !sessionStorage.getItem(tokenKey) || document.hidden) return;
  state.inFlight = true;
  try {
    if (state.page === 'status') renderStatus(await rpc('GetStatus'));
    if (state.page === 'flows') renderFlows(await rpc('GetActiveFlows'));
    if (state.page === 'config') document.querySelector('#config-json').textContent = JSON.stringify(await rpc('GetRuntimeConfig'), null, 2);
    showAlert('');
  } catch (error) { if (error.message !== 'unauthorized') showAlert(error.message); }
  finally { state.inFlight = false; }
}
function stopPolling() { if (state.timer) { clearInterval(state.timer); state.timer = 0; } }
function startPolling() { stopPolling(); if (!sessionStorage.getItem(tokenKey)) return; const interval = state.page === 'flows' ? 2000 : 5000; state.timer = setInterval(refreshPage, interval); refreshPage(); }
function selectPage(page) { state.page = page; for (const section of document.querySelectorAll('[data-section]')) section.hidden = section.id !== `page-${page}`; startPolling(); }

document.querySelector('#login-form').addEventListener('submit', (event) => { event.preventDefault(); sessionStorage.setItem(tokenKey, tokenInput.value); tokenInput.value = ''; setAuthenticated(true); startPolling(); });
document.querySelector('#logout').addEventListener('click', logout);
for (const button of document.querySelectorAll('[data-page]')) button.addEventListener('click', () => selectPage(button.dataset.page));
document.querySelector('#flow-filter').addEventListener('input', () => { if (state.page === 'flows') refreshPage(); });
document.querySelector('#mode-form').addEventListener('submit', async (event) => { event.preventDefault(); const mode = document.querySelector('#mode').value; const tag = document.querySelector('#manual-upstream').value; try { await rpc('SetUpstream', { mode, ...(mode === 'manual' ? { tag } : {}) }); await refreshPage(); } catch (error) { showAlert(error.message); } });
document.addEventListener('visibilitychange', () => { if (document.hidden) stopPolling(); else startPolling(); });
setAuthenticated(Boolean(sessionStorage.getItem(tokenKey))); if (sessionStorage.getItem(tokenKey)) startPolling();
