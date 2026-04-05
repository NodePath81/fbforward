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

function updateTopology(upstreams) {
  for (const entry of upstreams) {
    const label = document.querySelector(`#topology-${entry.upstream}`);
    if (!label) {
      continue;
    }
    const detail = entry.delay_ms > 0 || entry.loss_pct > 0
      ? `delay ${entry.delay_ms}ms / loss ${entry.loss_pct}%`
      : "none";
    label.textContent = `${entry.upstream}: ${detail}`;
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

  const errorEntries = Object.entries(payload.errors || {});
  errors.innerHTML = errorEntries.map(([name, message]) => `<p>${name}: ${message}</p>`).join("");

  if (payload.fbcoord) {
    const pick = payload.fbcoord.pick || {};
    fbcoordCard.innerHTML = `
      <div class="stat-line"><strong>Pool:</strong> ${payload.fbcoord.pool}</div>
      <div class="stat-line"><strong>Pick:</strong> ${pick.upstream || "none"}</div>
      <div class="stat-line"><strong>Version:</strong> ${pick.version ?? 0}</div>
      <div class="stat-line"><strong>Nodes:</strong> ${payload.fbcoord.node_count}</div>
    `;
  } else {
    fbcoordCard.innerHTML = `<p class="subtle">fbcoord pool data unavailable.</p>`;
  }

  nodeCards.innerHTML = ["node-1", "node-2"].map((nodeName) => {
    const node = payload.nodes ? payload.nodes[nodeName] : null;
    if (!node) {
      return `<article class="node-card"><h3>${nodeName}</h3><p class="subtle">No data.</p></article>`;
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

function renderShaping(payload) {
  const list = document.querySelector("#shaping-list");
  const errorBox = document.querySelector("#shaping-error");
  errorBox.innerHTML = "";

  const upstreams = payload.upstreams || [];
  updateTopology(upstreams);
  list.innerHTML = upstreams.map((entry) => `
    <article class="shape-card" data-upstream="${entry.upstream}">
      <div class="shape-header">
        <h3>${entry.upstream}</h3>
        <span class="chip">${entry.tag}</span>
      </div>
      <p class="subtle">${entry.namespace} / ${entry.device}</p>
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

async function applyShaping(upstream, delayMs, lossPct) {
  try {
    const payload = await requestJson(`/api/shaping/${encodeURIComponent(upstream)}`, {
      method: "PUT",
      body: JSON.stringify({ delay_ms: delayMs, loss_pct: lossPct }),
    });
    renderShaping(payload);
    await loadCoordination();
  } catch (error) {
    document.querySelector("#shaping-error").innerHTML = `<p>${error.message}</p>`;
  }
}

async function clearShaping(upstream) {
  try {
    const payload = await requestJson(`/api/shaping/${encodeURIComponent(upstream)}`, {
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
  if (value === "healthy") {
    await clearAllShaping();
    return;
  }
  await clearAllShaping();
  if (value === "upstream-1-delay") {
    await applyShaping("upstream-1", 200, 0);
  } else if (value === "upstream-2-delay") {
    await applyShaping("upstream-2", 200, 0);
  } else if (value === "both-delay") {
    await applyShaping("upstream-1", 200, 0);
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
    const card = button.closest("[data-upstream]");
    if (!card) {
      return;
    }
    const upstream = card.dataset.upstream;
    if (button.dataset.action === "clear") {
      void clearShaping(upstream);
      return;
    }
    if (button.dataset.action === "apply") {
      const delay = Number(card.querySelector('[data-field="delay"]').value || "0");
      const loss = Number(card.querySelector('[data-field="loss"]').value || "0");
      void applyShaping(upstream, delay, loss);
    }
  });
}

document.addEventListener("DOMContentLoaded", async () => {
  bindEvents();
  await refreshAll();
  schedulePolling();
});
