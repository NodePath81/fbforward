const FIXED_TARGET_ORDER = ["fbcoord", "fbnotify", "node-1", "node-2", "upstream-1", "upstream-2"];
const KIND_ORDER = {
  service: 0,
  node: 1,
  upstream: 2,
  client: 3,
};
const TOPOLOGY_FIXED_IDS = {
  fbcoord: { label: "topology-fbcoord", line: "topology-link-fbcoord", group: "topology-target-fbcoord" },
  fbnotify: { label: "topology-fbnotify", line: "topology-link-fbnotify", group: "topology-target-fbnotify" },
  "node-1": { label: "topology-node-1", line: "topology-link-node-1", group: "topology-target-node-1" },
  "node-2": { label: "topology-node-2", line: "topology-link-node-2", group: "topology-target-node-2" },
  "upstream-1": { label: "topology-upstream-1", line: "topology-link-upstream-1", group: "topology-target-upstream-1" },
  "upstream-2": { label: "topology-upstream-2", line: "topology-link-upstream-2", group: "topology-target-upstream-2" },
};

const state = {
  refreshMs: 5000,
  timer: null,
  clientMutationInFlight: false,
  clients: {},
  fbnotify: null,
  processes: [],
  shapingTargets: [],
  linkTargets: {},
};

async function requestJson(url, options = {}) {
  const response = await fetch(url, {
    headers: {
      "Content-Type": "application/json",
      ...(options.headers || {}),
    },
    ...options,
  });
  let payload = null;
  try {
    payload = await response.json();
  } catch {
    payload = null;
  }
  if (!response.ok) {
    const message = payload && payload.error ? payload.error : `${response.status} ${response.statusText}`;
    throw new Error(message);
  }
  return payload;
}

function renderBanner(message) {
  const banner = document.querySelector("#global-error");
  if (!message) {
    banner.classList.add("hidden");
    banner.textContent = "";
    return;
  }
  banner.textContent = message;
  banner.classList.remove("hidden");
}

function statusBadge(alive) {
  return alive ? `<span class="badge ok">alive</span>` : `<span class="badge bad">dead</span>`;
}

function renderList(containerId, items, formatter) {
  const element = document.querySelector(containerId);
  if (!items.length) {
    element.innerHTML = `<p class="subtle">No entries.</p>`;
    return;
  }
  element.innerHTML = items.map(formatter).join("");
}

function shapeStateText(entry, linkState) {
  if (linkState && linkState.connected === false) {
    return "disconnected";
  }
  const parts = [];
  if (entry.delay_ms > 0) {
    parts.push(`${entry.delay_ms}ms delay`);
  }
  if (entry.loss_pct > 0) {
    parts.push(`${entry.loss_pct}% loss`);
  }
  return parts.length ? parts.join(" • ") : "healthy";
}

function targetKindOrder(entry) {
  return Object.prototype.hasOwnProperty.call(KIND_ORDER, entry.kind) ? KIND_ORDER[entry.kind] : 99;
}

function sortTargets(entries) {
  return [...entries].sort((left, right) => {
    const kindDelta = targetKindOrder(left) - targetKindOrder(right);
    if (kindDelta !== 0) {
      return kindDelta;
    }
    return (left.display_name || left.target).localeCompare(right.display_name || right.target);
  });
}

function topologyTargetText(targetName, linkState, shapingEntry) {
  if (linkState && linkState.connected === false) {
    return "disconnected";
  }
  if (shapingEntry) {
    return shapeStateText(shapingEntry, linkState);
  }
  return "connected";
}

function renderDynamicClientTopology(clientTargets) {
  const container = document.querySelector("#topology-clients");
  if (!container) {
    return;
  }
  if (!clientTargets.length) {
    container.innerHTML = "";
    return;
  }
  const width = 132;
  const height = 48;
  const gap = 24;
  const centerX = 510;
  const rowY = 386;
  const totalWidth = clientTargets.length * width + Math.max(0, clientTargets.length - 1) * gap;
  const startX = centerX - totalWidth / 2;
  container.innerHTML = clientTargets.map((entry, index) => {
    const x = startX + index * (width + gap);
    const y = rowY;
    const center = x + width / 2;
    const disconnected = entry.connected === false;
    const groupClass = disconnected ? "client-node offline" : "client-node";
    const lineClass = disconnected ? "link branch offline" : "link branch";
    return `
      <g id="topology-link-${entry.target}" class="${lineClass}">
        <line x1="510" y1="372" x2="${center}" y2="${y}"></line>
      </g>
      <g id="topology-target-${entry.target}" class="${groupClass}">
        <rect class="target-box" x="${x}" y="${y}" width="${width}" height="${height}" rx="14"></rect>
        <text x="${center}" y="${y + 16}" class="box-label">${entry.display_name || entry.target}</text>
        <rect class="target-pill" x="${x + 14}" y="${y + 24}" width="${width - 28}" height="18" rx="9"></rect>
        <text x="${center}" y="${y + 33}" class="shape-label">${disconnected ? "disconnected" : "connected"}</text>
      </g>
    `;
  }).join("");
}

function updateTopology(linkTargets, shapingTargets) {
  const byTarget = Object.fromEntries(shapingTargets.map((entry) => [entry.target, entry]));
  for (const target of FIXED_TARGET_ORDER) {
    const ids = TOPOLOGY_FIXED_IDS[target];
    const label = document.querySelector(`#${ids.label}`);
    const line = document.querySelector(`#${ids.line}`);
    const group = document.querySelector(`#${ids.group}`);
    const linkState = linkTargets[target];
    if (!label) {
      continue;
    }
    label.textContent = topologyTargetText(target, linkState, byTarget[target]);
    const disconnected = linkState ? linkState.connected === false : false;
    if (line) {
      line.classList.toggle("offline", disconnected);
    }
    if (group) {
      group.classList.toggle("offline", disconnected);
    }
  }
  renderDynamicClientTopology(
    sortTargets(Object.values(linkTargets || {}).filter((entry) => entry.kind === "client")),
  );
}

function syncLogProcessOptions(processes) {
  const select = document.querySelector("#log-process");
  const previous = select.value;
  select.innerHTML = "";
  for (const process of processes) {
    const option = document.createElement("option");
    option.value = process.name;
    option.textContent = process.name;
    select.appendChild(option);
  }
  if (previous && processes.some((process) => process.name === previous)) {
    select.value = previous;
  }
}

function renderServiceLinks(serviceLinks) {
  const container = document.querySelector("#service-links");
  const entries = Object.entries(serviceLinks || {});
  if (!entries.length) {
    container.innerHTML = `<p class="subtle">No host service links available.</p>`;
    return;
  }
  container.innerHTML = entries.map(([name, url]) => `
    <a class="service-card" href="${url}" target="_blank" rel="noreferrer">
      <span class="service-name">${name}</span>
      <span class="service-url">${url}</span>
    </a>
  `).join("");
}

function renderNotifications(messages = [], error = "") {
  const errorBox = document.querySelector("#notifications-error");
  const list = document.querySelector("#notifications-list");
  const openLink = document.querySelector("#notifications-open");
  const fbnotifyUrl = state.fbnotify && state.fbnotify.available ? state.fbnotify.public_url : "";

  errorBox.innerHTML = error ? `<p>${error}</p>` : "";
  if (fbnotifyUrl) {
    openLink.href = fbnotifyUrl;
    openLink.classList.remove("hidden");
  } else {
    openLink.href = "#";
    openLink.classList.add("hidden");
  }

  if (error) {
    list.innerHTML = `<p class="subtle">Notification data unavailable.</p>`;
    return;
  }
  if (!messages.length) {
    list.innerHTML = `<p class="subtle">No captured notifications.</p>`;
    return;
  }
  list.innerHTML = messages.map((message) => `
    <div class="list-row">
      <div>
        <strong>${message.event_name}</strong>
        <div class="subtle">${message.source_service}/${message.source_instance}</div>
      </div>
      <div class="subtle">${message.severity} • ${message.received_at}</div>
    </div>
  `).join("");
}

function renderClientManagement(clients) {
  const entries = Object.entries(clients || {});
  renderList("#client-management-list", entries, ([name, info]) => `
    <div class="list-row">
      <div>
        <strong>${name}</strong>
        <span class="subtle">client namespace</span>
      </div>
      <div class="list-actions">
        <span class="subtle">${info.identity_ip}</span>
        <button
          type="button"
          class="ghost warn-button"
          data-client-action="remove"
          data-client-name="${name}"
          ${state.clientMutationInFlight ? "disabled" : ""}
        >
          Remove
        </button>
      </div>
    </div>
  `);
}

function renderClientPaths(clients) {
  const entries = Object.entries(clients || {});
  renderList("#client-paths", entries, ([name, info]) => `
    <div class="list-row">
      <div>
        <strong>${name}</strong>
        <span class="subtle">${info.identity_ip}</span>
      </div>
      <div class="subtle">client -> client-edge -> internet -> hub</div>
    </div>
  `);
}

function renderTerminalLinks(terminals) {
  const container = document.querySelector("#terminal-links");
  const entries = Object.entries(terminals || {});
  if (!entries.length) {
    container.innerHTML = `<p class="subtle">No interactive terminals available.</p>`;
    return;
  }
  container.innerHTML = entries.map(([name, info]) => `
    <a class="service-card" href="${info.url}" target="_blank" rel="noreferrer">
      <span class="service-name">${info.label || name}</span>
      <span class="service-url">${info.url}</span>
      <span class="service-url">${info.alive ? "alive" : "dead"} • pid ${info.pid}</span>
    </a>
  `).join("");
}

function renderClientMutationMessage(message, kind = "info") {
  const element = document.querySelector("#client-mutation-message");
  if (!message) {
    element.classList.add("hidden");
    element.classList.remove("error-list");
    element.classList.remove("success-note");
    element.textContent = "";
    return;
  }
  element.textContent = message;
  element.classList.remove("hidden");
  element.classList.toggle("error-list", kind === "error");
  element.classList.toggle("success-note", kind === "success");
}

function setClientControlsDisabled(disabled) {
  state.clientMutationInFlight = disabled;
  for (const selector of ["#client-name", "#client-identity-ip", "#client-add"]) {
    const element = document.querySelector(selector);
    if (element) {
      element.disabled = disabled;
    }
  }
  for (const button of document.querySelectorAll("[data-client-action='remove']")) {
    button.disabled = disabled;
  }
}

function renderCoordination(payload) {
  const errors = document.querySelector("#coord-errors");
  const fbcoordCard = document.querySelector("#fbcoord-card");
  const nodeCards = document.querySelector("#node-cards");
  const errorMap = payload.errors || {};

  const errorEntries = Object.entries(errorMap);
  errors.innerHTML = errorEntries.map(([name, message]) => `<p>${name}: ${message}</p>`).join("");

  if (payload.fbcoord) {
    const pick = payload.fbcoord.pick || {};
    const connectedNodes = Array.isArray(payload.fbcoord.nodes)
      ? payload.fbcoord.nodes.map((node) => node.node_id).filter(Boolean)
      : [];
    fbcoordCard.innerHTML = `
      <div class="stat-line"><strong>Pick:</strong> ${pick.upstream || "none"}</div>
      <div class="stat-line"><strong>Version:</strong> ${pick.version ?? 0}</div>
      <div class="stat-line"><strong>Nodes:</strong> ${payload.fbcoord.node_count}</div>
      <div class="stat-line"><strong>Connected:</strong> ${connectedNodes.length ? connectedNodes.join(", ") : "none"}</div>
    `;
  } else if (errorMap.fbcoord) {
    fbcoordCard.innerHTML = `<p class="subtle">${errorMap.fbcoord}</p>`;
  } else {
    fbcoordCard.innerHTML = `<p class="subtle">fbcoord state data unavailable.</p>`;
  }

  nodeCards.innerHTML = ["node-1", "node-2"].map((nodeName) => {
    const node = payload.nodes ? payload.nodes[nodeName] : null;
    const nodeError = errorMap[nodeName];
    if (!node) {
      return `
        <article class="node-card">
          <h3>${nodeName}</h3>
          <p class="subtle">${nodeError || "No data."}</p>
        </article>
      `;
    }
    const coordination = node.coordination || {};
    return `
      <article class="node-card">
        <h3>${nodeName}</h3>
        <p><strong>Mode:</strong> ${node.mode}</p>
        <p><strong>Active:</strong> ${node.active_upstream || "none"}</p>
        <p><strong>Connected:</strong> ${coordination.connected ? "yes" : "no"}</p>
        <p><strong>Authoritative:</strong> ${coordination.authoritative ? "yes" : "no"}</p>
        <p><strong>Selected:</strong> ${coordination.selected_upstream || "none"}</p>
        <p><strong>Fallback:</strong> ${coordination.fallback_active ? "yes" : "no"}</p>
      </article>
    `;
  }).join("");
}

function targetAxis(entry) {
  if (entry.kind === "service") {
    return "service / hub";
  }
  if (entry.kind === "node") {
    return "node-side / hub";
  }
  if (entry.kind === "upstream") {
    return "upstream-side / hub-up";
  }
  return "client-side / client-edge";
}

function targetEffect(entry) {
  if (entry.kind === "service") {
    return "Service control link to hub. Connect and disconnect only.";
  }
  if (entry.kind === "node") {
    return "Affects this node broadly, including coordination traffic.";
  }
  if (entry.kind === "upstream") {
    return "Affects both nodes only when they use this upstream.";
  }
  return "Client transport link to client-edge. Connect and disconnect only.";
}

function linkStateBadge(connected) {
  return connected
    ? `<span class="badge ok">connected</span>`
    : `<span class="badge bad">disconnected</span>`;
}

function renderShapingView() {
  const list = document.querySelector("#shaping-list");
  const linkTargets = sortTargets(Object.values(state.linkTargets || {}));
  const shapingByTarget = Object.fromEntries((state.shapingTargets || []).map((entry) => [entry.target, entry]));
  updateTopology(state.linkTargets || {}, state.shapingTargets || []);
  list.innerHTML = linkTargets.map((entry) => {
    const shapingEntry = shapingByTarget[entry.target] || { delay_ms: 0, loss_pct: 0 };
    const linkState = state.linkTargets[entry.target] || entry || { connected: true };
    const connected = linkState.connected !== false;
    const toggleLabel = connected ? "Disconnect" : "Reconnect";
    const toggleClass = connected ? "ghost warn-button" : "";
    const shapingControls = entry.shape_capable ? `
      <div class="shape-values">
        <label>
          Delay (ms)
          <input type="number" min="0" value="${shapingEntry.delay_ms}" data-field="delay">
        </label>
        <label>
          Loss (%)
          <input type="number" min="0" max="100" step="0.1" value="${shapingEntry.loss_pct}" data-field="loss">
        </label>
      </div>
      <div class="inline-actions">
        <button type="button" data-action="apply">Apply</button>
        <button type="button" class="ghost" data-action="clear">Clear</button>
        <button type="button" class="${toggleClass}" data-action="link" data-connected="${connected ? "true" : "false"}">${toggleLabel}</button>
      </div>
      ${connected ? "" : `<p class="subtle">Pending shaping will be re-applied on reconnect.</p>`}
    ` : `
      <div class="inline-actions">
        <button type="button" class="${toggleClass}" data-action="link" data-connected="${connected ? "true" : "false"}">${toggleLabel}</button>
      </div>
    `;
    return `
      <article class="shape-card ${connected ? "" : "shape-card-offline"}" data-target="${entry.target}">
        <div class="shape-header">
          <h3>${entry.display_name || entry.target}</h3>
          <div class="chip-row">
            ${linkStateBadge(connected)}
            <span class="chip muted-chip">${targetAxis(entry)}</span>
            ${entry.kind ? `<span class="chip">${entry.kind}</span>` : ""}
            ${entry.tag ? `<span class="chip">${entry.tag}</span>` : ""}
          </div>
        </div>
        <p class="subtle">${entry.namespace} / ${entry.device}</p>
        <p class="shape-note">${targetEffect(entry)}</p>
        ${shapingControls}
      </article>
    `;
  }).join("");
}

function renderShaping(payload) {
  const errorBox = document.querySelector("#shaping-error");
  errorBox.innerHTML = "";
  state.shapingTargets = payload.targets || [];
  renderShapingView();
}

function applyStatusPayload(payload) {
  document.querySelector("#lab-phase").textContent = payload.phase ? `phase ${payload.phase}` : "inactive";
  document.querySelector("#lab-meta").innerHTML = payload.active
    ? `<p><strong>Workdir:</strong> ${payload.work_dir}</p><p><strong>State:</strong> ${payload.state_path}</p>`
    : `<p class="subtle">${payload.error}</p>`;
  renderBanner(payload.active ? "" : payload.error);

  renderList("#namespace-list", payload.namespaces || [], (entry) => `
    <div class="list-row">
      <div>
        <strong>${entry.name}</strong>
        <span class="subtle">${entry.role}</span>
      </div>
      <div>${statusBadge(entry.alive)}</div>
    </div>
  `);
  renderList("#process-list", payload.processes || [], (entry) => `
    <div class="list-row">
      <div>
        <strong>${entry.name}</strong>
        <span class="subtle">${entry.ns}</span>
      </div>
      <div>${statusBadge(entry.alive)}</div>
    </div>
  `);
  state.clients = payload.clients || {};
  state.fbnotify = payload.fbnotify || null;
  renderClientManagement(state.clients);
  renderClientPaths(state.clients);
  renderServiceLinks(payload.service_links || {});
  renderTerminalLinks(payload.terminals || {});
  if (!state.fbnotify || !state.fbnotify.available) {
    const message = state.fbnotify && state.fbnotify.error
      ? state.fbnotify.error
      : "fbnotify is not available for this lab run.";
    renderNotifications([], message);
  }
  state.processes = payload.processes || [];
  syncLogProcessOptions(state.processes);
}

async function loadStatus() {
  const payload = await requestJson("/api/status");
  applyStatusPayload(payload);
  return payload;
}

async function loadCoordination() {
  try {
    const payload = await requestJson("/api/coordination");
    renderCoordination(payload);
  } catch (error) {
    renderCoordination({ fbcoord: null, nodes: {}, errors: { fbcoord: error.message } });
  }
}

async function loadShaping() {
  try {
    const payload = await requestJson("/api/shaping");
    renderShaping(payload);
  } catch (error) {
    document.querySelector("#shaping-error").innerHTML = `<p>${error.message}</p>`;
  }
}

async function loadLinkState() {
  try {
    const payload = await requestJson("/api/link-state");
    state.linkTargets = Object.fromEntries((payload.targets || []).map((entry) => [entry.target, entry]));
    renderShapingView();
  } catch (error) {
    document.querySelector("#shaping-error").innerHTML = `<p>${error.message}</p>`;
  }
}

async function loadLogs() {
  const process = document.querySelector("#log-process").value;
  const lines = document.querySelector("#log-lines").value || "100";
  if (!process) {
    return;
  }
  try {
    const payload = await requestJson(`/api/logs/${encodeURIComponent(process)}?lines=${encodeURIComponent(lines)}`);
    document.querySelector("#log-error").innerHTML = "";
    document.querySelector("#log-output").textContent = payload.text || "(no log output)";
  } catch (error) {
    document.querySelector("#log-error").innerHTML = `<p>${error.message}</p>`;
  }
}

async function loadNotifications() {
  if (!state.fbnotify || !state.fbnotify.available) {
    const message = state.fbnotify && state.fbnotify.error
      ? state.fbnotify.error
      : "fbnotify is not available for this lab run.";
    renderNotifications([], message);
    return;
  }
  try {
    const payload = await requestJson("/api/ntfybox");
    renderNotifications(payload.messages || []);
  } catch (error) {
    renderNotifications([], error.message);
  }
}

async function clearNotifications() {
  try {
    await requestJson("/api/ntfybox", { method: "DELETE" });
    await loadNotifications();
  } catch (error) {
    renderNotifications([], error.message);
  }
}

async function applyShaping(target, delayMs, lossPct) {
  try {
    const payload = await requestJson(`/api/shaping/${encodeURIComponent(target)}`, {
      method: "PUT",
      body: JSON.stringify({ delay_ms: delayMs, loss_pct: lossPct }),
    });
    renderShaping(payload);
    await loadCoordination();
  } catch (error) {
    document.querySelector("#shaping-error").innerHTML = `<p>${error.message}</p>`;
  }
}

async function clearShaping(target) {
  try {
    const payload = await requestJson(`/api/shaping/${encodeURIComponent(target)}`, {
      method: "DELETE",
    });
    renderShaping(payload);
    await loadCoordination();
  } catch (error) {
    document.querySelector("#shaping-error").innerHTML = `<p>${error.message}</p>`;
  }
}

async function clearAllShaping() {
  try {
    const payload = await requestJson("/api/shaping", { method: "DELETE" });
    renderShaping(payload);
    await loadCoordination();
  } catch (error) {
    document.querySelector("#shaping-error").innerHTML = `<p>${error.message}</p>`;
  }
}

async function setLinkConnected(target, connected) {
  try {
    const payload = await requestJson(`/api/link-state/${encodeURIComponent(target)}`, {
      method: "PUT",
      body: JSON.stringify({ connected }),
    });
    state.linkTargets = Object.fromEntries((payload.targets || []).map((entry) => [entry.target, entry]));
    renderShapingView();
    await loadCoordination();
  } catch (error) {
    document.querySelector("#shaping-error").innerHTML = `<p>${error.message}</p>`;
  }
}

async function mutateClient(url, options, successMessage) {
  setClientControlsDisabled(true);
  renderClientMutationMessage("");
  try {
    const payload = await requestJson(url, options);
    applyStatusPayload(payload);
    renderClientMutationMessage(successMessage, "success");
    await loadCoordination();
    await loadShaping();
    await loadLinkState();
    return true;
  } catch (error) {
    renderClientMutationMessage(error.message, "error");
    return false;
  } finally {
    setClientControlsDisabled(false);
    renderClientManagement(state.clients);
  }
}

async function addClient(event) {
  event.preventDefault();
  const name = document.querySelector("#client-name").value.trim();
  const identityIp = document.querySelector("#client-identity-ip").value.trim();
  if (!name || !identityIp) {
    renderClientMutationMessage("name and identity_ip are required", "error");
    return;
  }
  const ok = await mutateClient(
    "/api/clients",
    {
      method: "POST",
      body: JSON.stringify({ name, identity_ip: identityIp }),
    },
    `Added ${name}`,
  );
  if (ok) {
    document.querySelector("#client-form").reset();
  }
}

async function removeClient(name) {
  await mutateClient(
    `/api/clients/${encodeURIComponent(name)}`,
    { method: "DELETE" },
    `Removed ${name}`,
  );
}

async function applyPreset() {
  const value = document.querySelector("#preset-select").value;
  if (!value) {
    return;
  }
  await clearAllShaping();
  if (value === "healthy") {
    return;
  }
  if (value === "node-1-delay") {
    await applyShaping("node-1", 200, 0);
  } else if (value === "upstream-1-delay") {
    await applyShaping("upstream-1", 200, 0);
  } else if (value === "node-1-upstream-2-delay") {
    await applyShaping("node-1", 200, 0);
    await applyShaping("upstream-2", 200, 0);
  }
}

async function refreshAll() {
  await loadStatus();
  await loadCoordination();
  await loadShaping();
  await loadLinkState();
  await loadNotifications();
}

function schedulePolling() {
  if (state.timer) {
    clearInterval(state.timer);
  }
  state.timer = setInterval(() => {
    if (!document.hidden) {
      void refreshAll();
    }
  }, state.refreshMs);
}

function bindEvents() {
  document.querySelector("#refresh-now").addEventListener("click", () => void refreshAll());
  document.querySelector("#refresh-coordination").addEventListener("click", () => void loadCoordination());
  document.querySelector("#refresh-logs").addEventListener("click", () => void loadLogs());
  document.querySelector("#notifications-clear").addEventListener("click", () => void clearNotifications());
  document.querySelector("#clear-all").addEventListener("click", () => void clearAllShaping());
  document.querySelector("#apply-preset").addEventListener("click", () => void applyPreset());
  document.querySelector("#client-form").addEventListener("submit", (event) => void addClient(event));
  document.querySelector("#refresh-interval").addEventListener("change", (event) => {
    state.refreshMs = Number(event.target.value);
    schedulePolling();
  });
  document.querySelector("#client-management-list").addEventListener("click", (event) => {
    const button = event.target.closest("[data-client-action='remove']");
    if (!button) {
      return;
    }
    void removeClient(button.dataset.clientName);
  });
  document.querySelector("#shaping-list").addEventListener("click", (event) => {
    const button = event.target.closest("button");
    if (!button) {
      return;
    }
    const card = button.closest("[data-target]");
    if (!card) {
      return;
    }
    const target = card.dataset.target;
    if (button.dataset.action === "clear") {
      void clearShaping(target);
      return;
    }
    if (button.dataset.action === "link") {
      const connected = button.dataset.connected === "true";
      void setLinkConnected(target, !connected);
      return;
    }
    if (button.dataset.action === "apply") {
      const delay = Number(card.querySelector('[data-field="delay"]').value || "0");
      const loss = Number(card.querySelector('[data-field="loss"]').value || "0");
      void applyShaping(target, delay, loss);
    }
  });
}

document.addEventListener("DOMContentLoaded", async () => {
  bindEvents();
  await refreshAll();
  schedulePolling();
});
