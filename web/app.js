const tokenKey = 'fbforward.control_token';
const login = document.querySelector('#login');
const app = document.querySelector('#app');
const alertBox = document.querySelector('#alert');
const tokenInput = document.querySelector('#token');
const state = { page: 'status', timer: 0, inFlight: false, status: null, audit: null, auditOffset: 0 };

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

function auditParams() {
  return { entry_type: document.querySelector('#audit-entry').value, protocol: document.querySelector('#audit-protocol').value, reason: document.querySelector('#audit-reason').value, tag: document.querySelector('#audit-tag').value, limit: Number(document.querySelector('#audit-limit').value) || 50, offset: state.auditOffset, sort_by: 'recorded_at', sort_order: 'desc' };
}
function loadAuditURL() {
  const query = new URLSearchParams(location.search);
  for (const key of ['entry', 'protocol', 'reason', 'tag', 'limit']) { const node = document.querySelector(`#audit-${key}`); if (node && query.has(key)) node.value = query.get(key); }
  state.auditOffset = Number(query.get('offset')) || 0;
}
function saveAuditURL() {
  const p = auditParams(); const query = new URLSearchParams({ entry: p.entry_type, protocol: p.protocol, reason: p.reason, tag: p.tag, limit: String(p.limit), offset: String(p.offset) });
  history.replaceState(null, '', `${location.pathname}?${query}`);
}
function renderAudit(data, talkers) {
  state.audit = data;
  document.querySelector('#audit-count').textContent = `${data.total || 0} records; offset ${state.auditOffset}`;
  const rows = document.querySelector('#audit-rows'); rows.replaceChildren();
  for (const record of data.records || []) { const row = document.createElement('tr'); cell(row, record.entry_type); cell(row, record.ip); cell(row, record.protocol); cell(row, record.port); cell(row, record.recorded_at); cell(row, record.upstream || ''); cell(row, record.reason || record.close_reason || ''); cell(row, record.bytes_up == null ? '' : `${record.bytes_up} / ${record.bytes_down}`); rows.append(row); }
  const talkerRows = document.querySelector('#talker-rows'); talkerRows.replaceChildren();
  for (const talker of talkers || []) { const row = document.createElement('tr'); cell(row, talker.client_ip); cell(row, talker.bytes_up); cell(row, talker.bytes_down); cell(row, talker.bytes_total); cell(row, talker.flow_count); talkerRows.append(row); }
}

async function refreshPage() {
  if (state.inFlight || !sessionStorage.getItem(tokenKey) || document.hidden) return;
  state.inFlight = true;
  try {
    if (state.page === 'status') renderStatus(await rpc('GetStatus'));
    if (state.page === 'flows') renderFlows(await rpc('GetActiveFlows'));
    if (state.page === 'config') document.querySelector('#config-json').textContent = JSON.stringify(await rpc('GetRuntimeConfig'), null, 2);
    if (state.page === 'audit') { const params = auditParams(); const result = await rpc('QueryLogEvents', params); const talkers = await rpc('GetTopTalkers', { protocol: params.protocol, tag: params.tag, limit: Math.min(params.limit, 100) }); renderAudit(result, talkers); }
    showAlert('');
  } catch (error) { if (error.message !== 'unauthorized') showAlert(error.message); }
  finally { state.inFlight = false; }
}
function stopPolling() { if (state.timer) { clearInterval(state.timer); state.timer = 0; } }
function startPolling() { stopPolling(); if (!sessionStorage.getItem(tokenKey)) return; const interval = state.page === 'flows' ? 2000 : (state.page === 'audit' ? 0 : 5000); if (interval) state.timer = setInterval(refreshPage, interval); refreshPage(); }
function selectPage(page) { state.page = page; for (const section of document.querySelectorAll('[data-section]')) section.hidden = section.id !== `page-${page}`; startPolling(); }

document.querySelector('#login-form').addEventListener('submit', (event) => { event.preventDefault(); sessionStorage.setItem(tokenKey, tokenInput.value); tokenInput.value = ''; setAuthenticated(true); startPolling(); });
document.querySelector('#logout').addEventListener('click', logout);
for (const button of document.querySelectorAll('[data-page]')) button.addEventListener('click', () => selectPage(button.dataset.page));
document.querySelector('#flow-filter').addEventListener('input', () => { if (state.page === 'flows') refreshPage(); });
document.querySelector('#audit-form').addEventListener('submit', (event) => { event.preventDefault(); state.auditOffset = 0; saveAuditURL(); refreshPage(); });
document.querySelector('#audit-prev').addEventListener('click', () => { state.auditOffset = Math.max(0, state.auditOffset - (Number(document.querySelector('#audit-limit').value) || 50)); saveAuditURL(); refreshPage(); });
document.querySelector('#audit-next').addEventListener('click', () => { state.auditOffset += Number(document.querySelector('#audit-limit').value) || 50; saveAuditURL(); refreshPage(); });
document.querySelector('#audit-export').addEventListener('click', () => { if (!state.audit) return; const blob = new Blob([JSON.stringify(state.audit, null, 2)], { type: 'application/json' }); const link = document.createElement('a'); link.href = URL.createObjectURL(blob); link.download = 'fbforward-audit.json'; link.click(); URL.revokeObjectURL(link.href); });
document.querySelector('#mode-form').addEventListener('submit', async (event) => { event.preventDefault(); const mode = document.querySelector('#mode').value; const tag = document.querySelector('#manual-upstream').value; try { await rpc('SetUpstream', { mode, ...(mode === 'manual' ? { tag } : {}) }); await refreshPage(); } catch (error) { showAlert(error.message); } });
document.addEventListener('visibilitychange', () => { if (document.hidden) stopPolling(); else startPolling(); });
loadAuditURL();
setAuthenticated(Boolean(sessionStorage.getItem(tokenKey))); if (sessionStorage.getItem(tokenKey)) startPolling();
