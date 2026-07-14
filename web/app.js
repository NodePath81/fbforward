const tokenKey = 'fbforward.control_token';
const login = document.querySelector('#login');
const app = document.querySelector('#app');
const alertBox = document.querySelector('#alert');
const loginError = document.querySelector('#login-error');
const tokenInput = document.querySelector('#token');
const pages = new Set(['status', 'flows', 'config', 'audit', 'firewall']);
const pollingIntervals = { status: 5000, flows: 2000 };
const requestedPage = new URLSearchParams(location.search).get('page');
const state = { page: pages.has(requestedPage) ? requestedPage : 'status', timer: 0, inFlight: false, requestPage: '', refreshPending: false, auditPending: false, auditPendingQuery: '', auditGeneration: 0, auditView: 'table', status: null, identity: null, identityLoaded: false, routes: [], flows: { tcp: [], udp: [] }, audit: null };

function showAlert(message) { alertBox.textContent = message; alertBox.hidden = !message; }
function showLoginError(message) { loginError.textContent = message; loginError.hidden = !message; }
function setAuthenticated(value) { login.hidden = value; app.hidden = !value; if (value) loadIdentity(); else stopPolling(); }
function logout(message = '') { sessionStorage.removeItem(tokenKey); state.identity = null; state.identityLoaded = false; document.querySelector('#instance-summary').textContent = ''; setAuthenticated(false); tokenInput.value = ''; showAlert(''); showLoginError(message); }
function text(value) { return value == null ? '' : String(value); }
function cell(row, value) { const node = document.createElement('td'); node.textContent = text(value); row.append(node); }

async function rpc(method, params) {
  return requestJSON('/rpc', { method: 'POST', body: JSON.stringify({ method, ...(params == null ? {} : { params }) }) });
}
async function requestJSON(path, options = {}) {
  const headers = { Authorization: `Bearer ${sessionStorage.getItem(tokenKey) || ''}`, 'Content-Type': 'application/json' };
  const response = await fetch(path, { ...options, headers: { ...headers, ...(options.headers || {}) } });
  let payload = null;
  try { payload = await response.json(); } catch (_) { throw new Error(`HTTP ${response.status}`); }
  if (response.status === 401) { logout('invalid token'); throw new Error('unauthorized'); }
  if (!response.ok || !payload.ok) throw new Error(payload.error || `HTTP ${response.status}`);
  return payload.result;
}

function renderStatus(data) {
  state.status = data;
  const rows = document.querySelector('#upstream-rows'); rows.replaceChildren();
  for (const up of data.upstreams || []) { const row = document.createElement('tr'); cell(row, up.tag); cell(row, up.health_state); cell(row, up.rtt_ms); rows.append(row); }
}
function renderIdentity(data) {
  state.identity = data;
  const values = [data.hostname, data.version, ...(Array.isArray(data.ips) ? data.ips : [])].filter(Boolean);
  document.querySelector('#instance-summary').textContent = values.join(' · ');
}
async function loadIdentity() {
  if (state.identityLoaded) return;
  state.identityLoaded = true;
  try { renderIdentity(await requestJSON('/identity')); }
  catch (error) { if (error.message !== 'unauthorized') document.querySelector('#instance-summary').textContent = 'identity unavailable'; }
}
function currentRoute() { return state.routes.find((route) => route.route === document.querySelector('#route-name').value) || state.routes[0]; }
function routeState(route) { if (route.override_state === 'active') return 'overridden'; if (route.override_state === 'fallback') return 'fallback'; return route.strategy === 'adaptive' ? 'automatic' : 'configured'; }
function renderRoutes(data) {
  state.routes = Array.isArray(data) ? data : (data && Array.isArray(data.routes) ? data.routes : []);
  const routeSelect = document.querySelector('#route-name'); const upstreamSelect = document.querySelector('#route-upstream');
  const previousRoute = routeSelect.value; routeSelect.replaceChildren();
  for (const route of state.routes) { const option = document.createElement('option'); option.value = route.route; option.textContent = `${route.route} (${route.strategy})`; option.selected = route.route === previousRoute; routeSelect.append(option); }
  const route = currentRoute(); upstreamSelect.replaceChildren();
  for (const tag of (route && route.upstreams) || []) { const option = document.createElement('option'); option.value = tag; option.textContent = tag; option.selected = tag === (route.override_upstream || route.effective_upstream || route.default_upstream); upstreamSelect.append(option); }
  const rows = document.querySelector('#route-rows'); rows.replaceChildren();
  for (const item of state.routes) { const row = document.createElement('tr'); cell(row, item.route); cell(row, item.strategy); cell(row, item.effective_upstream || 'unavailable'); cell(row, item.override_upstream || ''); cell(row, routeState(item)); rows.append(row); }
}

function renderFlows(data) {
  state.flows = { tcp: data.tcp || [], udp: data.udp || [] };
  renderFlowTable();
}
function renderFlowTable() {
  const filter = document.querySelector('#flow-filter').value.toLowerCase();
  const rows = document.querySelector('#flow-rows'); rows.replaceChildren();
  for (const flow of [...state.flows.tcp, ...state.flows.udp]) {
    const haystack = [flow.id, flow.client_addr, flow.listener, flow.route, flow.upstream].join(' ').toLowerCase();
    if (filter && !haystack.includes(filter)) continue;
    const row = document.createElement('tr'); cell(row, flow.id); cell(row, flow.client_addr); cell(row, flow.listener); cell(row, flow.route); cell(row, flow.upstream); cell(row, `${flow.bytes_up} / ${flow.bytes_down}`); cell(row, flow.last_activity); rows.append(row);
  }
}

function loadAuditURL() {
  const query = new URLSearchParams(location.search);
  const input = document.querySelector('#audit-query');
  if (input && query.has('query')) input.value = query.get('query');
}
function saveAuditURL() {
  const query = new URLSearchParams({ page: 'audit' });
  const value = document.querySelector('#audit-query').value.trim();
  if (value) query.set('query', value);
  history.replaceState(null, '', `${location.pathname}?${query.toString()}`);
}
function renderAudit(data) {
  state.audit = data;
  const count = data && data.result && !Array.isArray(data.result) ? data.result.total : (data && Array.isArray(data.result) ? data.result.length : 0);
  document.querySelector('#audit-count').textContent = data ? `${count} rows · ${data.source}${state.auditDuration == null ? '' : ` · ${state.auditDuration.toFixed(0)} ms`}` : 'enter a query and press RUN';
  const head = document.querySelector('#audit-head'); const rows = document.querySelector('#audit-rows'); head.replaceChildren(); rows.replaceChildren();
  const raw = document.querySelector('#audit-raw'); raw.textContent = data ? JSON.stringify(data, null, 2) : ''; raw.hidden = state.auditView !== 'raw'; document.querySelector('#audit-table-view').hidden = state.auditView !== 'table'; document.querySelector('#audit-view-toggle').textContent = state.auditView === 'table' ? 'RAW' : 'TABLE';
  if (!data) return;
  const result = data.result; const records = Array.isArray(result) ? result : (result.records || []);
  const columns = Array.isArray(result) ? (data.source === 'top asns' ? ['asn', 'as_org', 'country', 'bytes_up', 'bytes_down', 'bytes_total', 'flow_count'] : ['client_ip', 'bytes_up', 'bytes_down', 'bytes_total', 'flow_count']) : ['entry_type', 'ip', 'protocol', 'port', 'recorded_at', 'upstream', 'reason', 'close_reason', 'bytes_up', 'bytes_down', 'flow_id'];
  const header = document.createElement('tr'); for (const column of columns) cell(header, column); head.append(header);
  for (const record of records) { const row = document.createElement('tr'); for (const column of columns) cell(row, record[column]); rows.append(row); }
}
function renderAuditError(message) {
  state.audit = null; state.auditDuration = null;
  document.querySelector('#audit-count').textContent = `error: ${message}`;
  document.querySelector('#audit-head').replaceChildren(); document.querySelector('#audit-rows').replaceChildren(); document.querySelector('#audit-raw').textContent = '';
}
async function optionalRPC(method, params) { try { return { value: await rpc(method, params) }; } catch (error) { return { error: error.message }; } }
function actionButton(label, message, action) { const button = document.createElement('button'); button.type = 'button'; button.textContent = label; button.addEventListener('click', async () => { if (!confirm(message)) return; try { await action(); await refreshFirewall(); showAlert(''); } catch (error) { showAlert(error.message); } }); return button; }
async function refreshFirewall() {
  const status = await optionalRPC('GetFirewallStatus');
  const policy = await optionalRPC('GetFirewallPolicy');
  const rules = await optionalRPC('ListOnlineRules', { include_expired: true });
  document.querySelector('#firewall-status').textContent = status.error ? `persistent policy: unavailable (${status.error})` : `persistent policy: ${status.value.state} · ${status.value.source || status.value.policy_file || 'none'} · generation ${status.value.generation}`;
  document.querySelector('#firewall-policy').textContent = policy.error ? `persistent policy unavailable: ${policy.error}` : JSON.stringify(policy.value, null, 2);
  const rows = document.querySelector('#online-rule-rows'); rows.replaceChildren();
  if (rules.error) { const row = document.createElement('tr'); cell(row, `online rules unavailable: ${rules.error}`); rows.append(row); return; }
  for (const rule of rules.value || []) { const row = document.createElement('tr'); cell(row, rule.rule_id); cell(row, rule.action); cell(row, JSON.stringify(rule.matcher)); cell(row, rule.priority); cell(row, rule.expires_at || ''); cell(row, `${rule.state}${rule.state_reason ? ` (${rule.state_reason})` : ''}`); const ops = document.createElement('td'); if (rule.state === 'active') ops.append(actionButton('Expire', `Expire online rule ${rule.rule_id}?`, () => rpc('ExpireOnlineRule', { rule_id: rule.rule_id }))); ops.append(' ', actionButton('Delete', `Delete online rule ${rule.rule_id}?`, () => rpc('DeleteOnlineRule', { rule_id: rule.rule_id }))); row.append(ops); rows.append(row); }
}

async function refreshPage() {
  if (state.inFlight) {
    if (state.page === 'audit') { state.auditPending = true; state.auditPendingQuery = document.querySelector('#audit-query').value.trim(); }
    else if (state.page !== state.requestPage) state.refreshPending = true;
    return;
  }
  if (!sessionStorage.getItem(tokenKey) || document.hidden) return;
  state.inFlight = true;
  state.requestPage = state.page;
  try {
    if (state.page === 'status') { const status = await rpc('GetStatus'); renderStatus(status); renderRoutes(status.routes); }
    if (state.page === 'flows') renderFlows(await rpc('GetActiveFlows'));
    if (state.page === 'config') document.querySelector('#config-json').textContent = JSON.stringify(await rpc('GetRuntimeConfig'), null, 2);
    if (state.page === 'audit') {
      const query = state.auditPendingQuery || document.querySelector('#audit-query').value.trim();
      const generation = state.auditGeneration;
      if (query) { const started = performance.now(); const data = await rpc('QueryAudit', { query }); state.auditDuration = performance.now() - started; if (generation === state.auditGeneration && state.page === 'audit' && query === document.querySelector('#audit-query').value.trim()) renderAudit(data); }
      else if (generation === state.auditGeneration) renderAudit(null);
    }
    if (state.page === 'firewall') await refreshFirewall();
    showAlert('');
  } catch (error) { if (state.requestPage === 'audit' && state.page === 'audit' && !state.auditPending) renderAuditError(error.message); if (error.message !== 'unauthorized') showAlert(error.message); }
  finally {
    state.inFlight = false;
    state.requestPage = '';
    if (state.auditPending || state.refreshPending) {
      state.auditPending = false;
      state.auditPendingQuery = '';
      state.refreshPending = false;
      queueMicrotask(refreshPage);
    }
  }
}
function stopPolling() { if (state.timer) { clearInterval(state.timer); state.timer = 0; } }
function startPolling() { stopPolling(); if (!sessionStorage.getItem(tokenKey)) return; const interval = pollingIntervals[state.page] || 0; if (interval) state.timer = setInterval(refreshPage, interval); refreshPage(); }
function showPage(page) { for (const section of document.querySelectorAll('[data-section]')) section.hidden = section.id !== `page-${page}`; for (const button of document.querySelectorAll('[data-page]')) { if (button.dataset.page === page) button.setAttribute('aria-current', 'page'); else button.removeAttribute('aria-current'); } }
function selectPage(page) { if (!pages.has(page)) return; state.page = page; const url = new URL(location.href); url.searchParams.set('page', page); history.replaceState(null, '', `${url.pathname}${url.search}`); showPage(page); startPolling(); }

document.querySelector('#login-form').addEventListener('submit', (event) => { event.preventDefault(); showLoginError(''); sessionStorage.setItem(tokenKey, tokenInput.value); tokenInput.value = ''; setAuthenticated(true); startPolling(); });
document.querySelector('#logout').addEventListener('click', logout);
for (const button of document.querySelectorAll('[data-page]')) button.addEventListener('click', () => selectPage(button.dataset.page));
document.querySelector('#flow-filter').addEventListener('input', () => { if (state.page === 'flows') renderFlowTable(); });
document.querySelector('#audit-form').addEventListener('submit', (event) => { event.preventDefault(); state.auditGeneration++; saveAuditURL(); refreshPage(); });
document.querySelector('#audit-clear').addEventListener('click', () => { state.auditGeneration++; state.auditPendingQuery = ''; document.querySelector('#audit-query').value = ''; state.audit = null; saveAuditURL(); renderAudit(null); });
document.querySelector('#audit-help-toggle').addEventListener('click', () => { const help = document.querySelector('#audit-help'); help.hidden = !help.hidden; });
document.querySelector('#audit-view-toggle').addEventListener('click', () => { state.auditView = state.auditView === 'table' ? 'raw' : 'table'; renderAudit(state.audit); });
document.querySelector('#audit-query').addEventListener('keydown', (event) => { if (event.key === 'Escape') { document.querySelector('#audit-help').hidden = true; event.currentTarget.focus(); } if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) { event.preventDefault(); state.auditGeneration++; saveAuditURL(); refreshPage(); } });
document.querySelector('#audit-export').addEventListener('click', () => { if (!state.audit) return; const blob = new Blob([JSON.stringify(state.audit, null, 2)], { type: 'application/json' }); const link = document.createElement('a'); link.href = URL.createObjectURL(blob); link.download = 'fbforward-audit.json'; link.click(); URL.revokeObjectURL(link.href); });
document.querySelector('#route-name').addEventListener('change', () => renderRoutes(state.routes));
document.querySelector('#route-override-form').addEventListener('submit', async (event) => { event.preventDefault(); try { await rpc('SetRouteOverride', { route: document.querySelector('#route-name').value, upstream: document.querySelector('#route-upstream').value }); await refreshPage(); showAlert(''); } catch (error) { showAlert(error.message); } });
document.querySelector('#route-override-clear').addEventListener('click', async () => { try { await rpc('ClearRouteOverride', { route: document.querySelector('#route-name').value }); await refreshPage(); showAlert(''); } catch (error) { showAlert(error.message); } });
document.querySelector('#firewall-reload').addEventListener('click', async () => { if (!confirm('Reload the persistent firewall policy file?')) return; try { await rpc('ReloadFirewallPolicy'); await refreshFirewall(); showAlert(''); } catch (error) { showAlert(error.message); await refreshFirewall(); } });
document.querySelector('#firewall-validate').addEventListener('click', async () => { try { await rpc('ValidateFirewallPolicy'); showAlert('persistent policy is valid'); } catch (error) { showAlert(`policy validation failed: ${error.message}`); } });
document.addEventListener('visibilitychange', () => { if (document.hidden) stopPolling(); else startPolling(); });
loadAuditURL();
showPage(state.page);
setAuthenticated(Boolean(sessionStorage.getItem(tokenKey))); if (sessionStorage.getItem(tokenKey)) startPolling();
