const TARGET_ORDER = ["node-1", "node-2", "upstream-1", "upstream-2"];
const TARGET_LABELS = {
  "node-1": "node-1",
  "node-2": "node-2",
  "upstream-1": "upstream-1",
  "upstream-2": "upstream-2",
};
const TOPOLOGY_LABEL_IDS = {
  "node-1": "topology-node-1",
  "node-2": "topology-node-2",
  "upstream-1": "topology-upstream-1",
  "upstream-2": "topology-upstream-2",
};

const state = {
  refreshMs: 5000,
  timer: null,
  processes: [],
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

function shapeStateText(entry) {
  const parts = [];
  if (entry.delay_ms > 0) {
    parts.push(`${entry.delay_ms}ms delay`);
  }
  if (entry.loss_pct > 0) {
    parts.push(`${entry.loss_pct}% loss`);
  }
  return parts.length ? parts.join(" • ") : "healthy";
}

function sortTargets(entries) {
  return [...entries].sort((left, right) => TARGET_ORDER.indexOf(left.target) - TARGET_ORDER.indexOf(right.target));
}

function updateTopology(targets) {
  const byTarget = Object.fromEntries(targets.map((entry) => [entry.target, entry]));
  for (const target of TARGET_ORDER) {
    const label = document.querySelector(`#${TOPOLOGY_LABEL_IDS[target]}`);
    if (!label) {
      continue;
    }
    label.textContent = byTarget[target] ? shapeStateText(byTarget[target]) : "unknown";
  }
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

function renderCoordination(payload) {
  const errors = document.querySelector("#coord-errors");
  const fbcoordCard = document.querySelector("#fbcoord-card");
  const nodeCards = document.querySelector("#node-cards");
  const errorMap = payload.errors || {};

  const errorEntries = Object.entries(errorMap);
  errors.innerHTML = errorEntries.map(([name, message]) => `<p>${name}: ${message}</p>`).join("");

  if (payload.fbcoord) {
    const pick = payload.fbcoord.pick || {};
    fbcoordCard.innerHTML = `
      <div class="stat-line"><strong>Pool:</strong> ${payload.fbcoord.pool}</div>
      <div class="stat-line"><strong>Pick:</strong> ${pick.upstream || "none"}</div>
      <div class="stat-line"><strong>Version:</strong> ${pick.version ?? 0}</div>
      <div class="stat-line"><strong>Nodes:</strong> ${payload.fbcoord.node_count}</div>
    `;
  } else if (errorMap.fbcoord) {
    fbcoordCard.innerHTML = `<p class="subtle">${errorMap.fbcoord}</p>`;
  } else {
    fbcoordCard.innerHTML = `<p class="subtle">fbcoord pool data unavailable.</p>`;
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
        <p><strong>Selected:</strong> ${coordination.selected_upstream || "none"}</p>
        <p><strong>Fallback:</strong> ${coordination.fallback_active ? "yes" : "no"}</p>
      </article>
    `;
  }).join("");
}

function targetAxis(entry) {
  return entry.target.startsWith("node-") ? "node-side / hub" : "upstream-side / hub-up";
}

function targetEffect(entry) {
  if (entry.target.startsWith("node-")) {
    return "Affects this node broadly, including coordination traffic.";
  }
  return "Affects both nodes only when they use this upstream.";
}

function renderShaping(payload) {
  const list = document.querySelector("#shaping-list");
  const errorBox = document.querySelector("#shaping-error");
  errorBox.innerHTML = "";

  const targets = sortTargets(payload.targets || []);
  updateTopology(targets);
  list.innerHTML = targets.map((entry) => `
    <article class="shape-card" data-target="${entry.target}">
      <div class="shape-header">
        <h3>${TARGET_LABELS[entry.target] || entry.target}</h3>
        <div class="chip-row">
          <span class="chip muted-chip">${targetAxis(entry)}</span>
          ${entry.tag ? `<span class="chip">${entry.tag}</span>` : ""}
        </div>
      </div>
      <p class="subtle">${entry.namespace} / ${entry.device}</p>
      <p class="shape-note">${targetEffect(entry)}</p>
      <div class="shape-values">
        <label>
          Delay (ms)
          <input type="number" min="0" value="${entry.delay_ms}" data-field="delay">
        </label>
        <label>
          Loss (%)
          <input type="number" min="0" max="100" step="0.1" value="${entry.loss_pct}" data-field="loss">
        </label>
      </div>
      <div class="inline-actions">
        <button type="button" data-action="apply">Apply</button>
        <button type="button" class="ghost" data-action="clear">Clear</button>
      </div>
    </article>
  `).join("");
}

async function loadStatus() {
  const payload = await requestJson("/api/status");
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
  renderServiceLinks(payload.service_links || {});
  state.processes = payload.processes || [];
  syncLogProcessOptions(state.processes);
  return payload;
}

async function loadCoordination() {
  try {
    const payload = await requestJson("/api/coordination");
    renderCoordination(payload);
  } catch (error) {
    renderCoordination({ fbcoord: null, nodes: {}, errors: { coordination: error.message } });
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
  document.querySelector("#clear-all").addEventListener("click", () => void clearAllShaping());
  document.querySelector("#apply-preset").addEventListener("click", () => void applyPreset());
  document.querySelector("#refresh-interval").addEventListener("change", (event) => {
    state.refreshMs = Number(event.target.value);
    schedulePolling();
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
