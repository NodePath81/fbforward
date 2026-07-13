const tokenKey = 'fbforward.control_token';
const login = document.querySelector('#login');
const app = document.querySelector('#app');
const alertBox = document.querySelector('#alert');
const tokenInput = document.querySelector('#token');
const pages = new Set(['status', 'flows', 'config', 'audit', 'firewall']);
const requestedPage = new URLSearchParams(location.search).get('page');
const state = { page: pages.has(requestedPage) ? requestedPage : 'status', timer: 0, inFlight: false, requestPage: '', refreshPending: false, status: null, identity: null, routes: [], flows: { tcp: [], udp: [] }, audit: null, auditOffset: 0 };

function showAlert(message) { alertBox.textContent = message; alertBox.hidden = !message; }
function setAuthenticated(value) { login.hidden = value; app.hidden = !value; if (!value) stopPolling(); }
function logout() { sessionStorage.removeItem(tokenKey); setAuthenticated(false); tokenInput.value = ''; showAlert(''); }
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
  if (response.status === 401) { logout(); throw new Error('unauthorized'); }
  if (!response.ok || !payload.ok) throw new Error(payload.error || `HTTP ${response.status}`);
  return payload.result;
}

function renderStatus(data) {
  state.status = data;
  const summary = document.querySelector('#status-summary'); summary.replaceChildren();
  for (const [label, value] of [['mode', data.mode], ['active', data.active_upstream || 'none']]) { const item = document.createElement('span'); item.textContent = `${label}: ${text(value)}`; summary.append(item); }
  const rows = document.querySelector('#upstream-rows'); rows.replaceChildren();
  for (const up of data.upstreams || []) { const row = document.createElement('tr'); cell(row, up.tag); cell(row, up.health_state); cell(row, up.rtt_ms); cell(row, up.active ? 'yes' : 'no'); rows.append(row); }
}
function renderIdentity(data) {
  state.identity = data;
  document.querySelector('#identity-summary').textContent = `identity: ${text(data.hostname)} · version ${text(data.version)} · IPs ${Array.isArray(data.ips) && data.ips.length ? data.ips.join(', ') : 'none'}`;
}
function renderStatusExtras(schedule, iplog) {
  document.querySelector('#schedule-summary').textContent = schedule.error ? `schedule: unavailable (${schedule.error})` : `schedule: ${schedule.value.queue_length || 0} pending · next ${schedule.value.next_scheduled || 'none'}`;
  document.querySelector('#iplog-summary').textContent = iplog.error ? `ip-log: unavailable (${iplog.error})` : `ip-log: ${iplog.value.total_record_count || 0} records`;
}
function currentRoute() { return state.routes.find((route) => route.route === document.querySelector('#route-name').value) || state.routes[0]; }
function renderRoutes(data) {
  state.routes = Array.isArray(data) ? data : (data && Array.isArray(data.routes) ? data.routes : []);
  const routeSelect = document.querySelector('#route-name'); const upstreamSelect = document.querySelector('#route-upstream');
  const previousRoute = routeSelect.value; routeSelect.replaceChildren();
  for (const route of state.routes) { const option = document.createElement('option'); option.value = route.route; option.textContent = `${route.route} (${route.strategy})`; option.selected = route.route === previousRoute; routeSelect.append(option); }
  const route = currentRoute(); upstreamSelect.replaceChildren();
  for (const tag of (route && route.upstreams) || []) { const option = document.createElement('option'); option.value = tag; option.textContent = tag; option.selected = tag === (route.override_upstream || route.effective_upstream || route.default_upstream); upstreamSelect.append(option); }
  const rows = document.querySelector('#route-rows'); rows.replaceChildren();
  for (const item of state.routes) { const row = document.createElement('tr'); cell(row, item.route); cell(row, item.strategy); cell(row, (item.upstreams || []).join(', ')); cell(row, item.default_upstream || ''); cell(row, item.effective_upstream || 'unavailable'); cell(row, item.override_upstream || ''); cell(row, item.strategy === 'adaptive' && item.override_state === 'fallback' ? 'fallback' : (item.strategy === 'static' && item.override_upstream ? 'overridden' : (item.strategy === 'adaptive' ? 'automatic' : 'configured'))); rows.append(row); }
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

function auditParams() {
  const start = Number(document.querySelector('#audit-start').value); const end = Number(document.querySelector('#audit-end').value);
  return { entry_type: document.querySelector('#audit-entry').value, protocol: document.querySelector('#audit-protocol').value, cidr: document.querySelector('#audit-cidr').value, start_time: start || undefined, end_time: end || undefined, reason: document.querySelector('#audit-reason').value, tag: document.querySelector('#audit-tag').value, limit: Number(document.querySelector('#audit-limit').value) || 50, offset: state.auditOffset, sort_by: 'recorded_at', sort_order: 'desc' };
}
function loadAuditURL() {
  const query = new URLSearchParams(location.search);
  for (const key of ['entry', 'protocol', 'cidr', 'start', 'end', 'reason', 'tag', 'limit']) { const node = document.querySelector(`#audit-${key}`); if (node && query.has(key)) node.value = query.get(key); }
  state.auditOffset = Number(query.get('offset')) || 0;
}
function saveAuditURL() {
  const p = auditParams(); const query = new URLSearchParams({ page: 'audit', entry: p.entry_type, protocol: p.protocol, cidr: p.cidr, start: String(p.start_time || ''), end: String(p.end_time || ''), reason: p.reason, tag: p.tag, limit: String(p.limit), offset: String(p.offset) });
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
    if (state.page !== state.requestPage) state.refreshPending = true;
    return;
  }
  if (!sessionStorage.getItem(tokenKey) || document.hidden) return;
  state.inFlight = true;
  state.requestPage = state.page;
  try {
    if (state.page === 'status') { const [status, identity, schedule, iplog, routes] = await Promise.all([rpc('GetStatus'), requestJSON('/identity'), optionalRPC('GetScheduleStatus'), optionalRPC('GetIPLogStatus'), optionalRPC('GetRouteStatus')]); renderStatus(status); renderIdentity(identity); renderStatusExtras(schedule, iplog); renderRoutes(routes.error ? status.routes : routes.value); }
    if (state.page === 'flows') renderFlows(await rpc('GetActiveFlows'));
    if (state.page === 'config') document.querySelector('#config-json').textContent = JSON.stringify(await rpc('GetRuntimeConfig'), null, 2);
    if (state.page === 'audit') { const params = auditParams(); const result = await rpc('QueryLogEvents', params); const talkers = await rpc('GetTopTalkers', { protocol: params.protocol, tag: params.tag, limit: Math.min(params.limit, 100) }); renderAudit(result, talkers); }
    if (state.page === 'firewall') await refreshFirewall();
    showAlert('');
  } catch (error) { if (error.message !== 'unauthorized') showAlert(error.message); }
  finally {
    state.inFlight = false;
    state.requestPage = '';
    if (state.refreshPending) {
      state.refreshPending = false;
      queueMicrotask(refreshPage);
    }
  }
}
function stopPolling() { if (state.timer) { clearInterval(state.timer); state.timer = 0; } }
function startPolling() { stopPolling(); if (!sessionStorage.getItem(tokenKey)) return; const interval = state.page === 'flows' ? 2000 : (state.page === 'audit' ? 0 : 5000); if (interval) state.timer = setInterval(refreshPage, interval); refreshPage(); }
function selectPage(page) { if (!pages.has(page)) return; state.page = page; const url = new URL(location.href); url.searchParams.set('page', page); history.replaceState(null, '', `${url.pathname}${url.search}`); for (const section of document.querySelectorAll('[data-section]')) section.hidden = section.id !== `page-${page}`; startPolling(); }

document.querySelector('#login-form').addEventListener('submit', (event) => { event.preventDefault(); sessionStorage.setItem(tokenKey, tokenInput.value); tokenInput.value = ''; setAuthenticated(true); startPolling(); });
document.querySelector('#logout').addEventListener('click', logout);
for (const button of document.querySelectorAll('[data-page]')) button.addEventListener('click', () => selectPage(button.dataset.page));
document.querySelector('#flow-filter').addEventListener('input', () => { if (state.page === 'flows') renderFlowTable(); });
document.querySelector('#audit-form').addEventListener('submit', (event) => { event.preventDefault(); state.auditOffset = 0; saveAuditURL(); refreshPage(); });
document.querySelector('#audit-prev').addEventListener('click', () => { state.auditOffset = Math.max(0, state.auditOffset - (Number(document.querySelector('#audit-limit').value) || 50)); saveAuditURL(); refreshPage(); });
document.querySelector('#audit-next').addEventListener('click', () => { state.auditOffset += Number(document.querySelector('#audit-limit').value) || 50; saveAuditURL(); refreshPage(); });
document.querySelector('#audit-export').addEventListener('click', () => { if (!state.audit) return; const blob = new Blob([JSON.stringify(state.audit, null, 2)], { type: 'application/json' }); const link = document.createElement('a'); link.href = URL.createObjectURL(blob); link.download = 'fbforward-audit.json'; link.click(); URL.revokeObjectURL(link.href); });
document.querySelector('#route-name').addEventListener('change', () => renderRoutes(state.routes));
document.querySelector('#route-override-form').addEventListener('submit', async (event) => { event.preventDefault(); try { await rpc('SetRouteOverride', { route: document.querySelector('#route-name').value, upstream: document.querySelector('#route-upstream').value }); await refreshPage(); showAlert(''); } catch (error) { showAlert(error.message); } });
document.querySelector('#route-override-clear').addEventListener('click', async () => { try { await rpc('ClearRouteOverride', { route: document.querySelector('#route-name').value }); await refreshPage(); showAlert(''); } catch (error) { showAlert(error.message); } });
document.querySelector('#firewall-reload').addEventListener('click', async () => { if (!confirm('Reload the persistent firewall policy file?')) return; try { await rpc('ReloadFirewallPolicy'); await refreshFirewall(); showAlert(''); } catch (error) { showAlert(error.message); await refreshFirewall(); } });
document.querySelector('#firewall-validate').addEventListener('click', async () => { try { await rpc('ValidateFirewallPolicy'); showAlert('persistent policy is valid'); } catch (error) { showAlert(`policy validation failed: ${error.message}`); } });
document.addEventListener('visibilitychange', () => { if (document.hidden) stopPolling(); else startPolling(); });
loadAuditURL();
for (const section of document.querySelectorAll('[data-section]')) section.hidden = section.id !== `page-${state.page}`;
setAuthenticated(Boolean(sessionStorage.getItem(tokenKey))); if (sessionStorage.getItem(tokenKey)) startPolling();
