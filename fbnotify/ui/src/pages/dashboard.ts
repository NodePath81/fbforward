import { appState } from '../state.js';
import type {
  CaptureMessage,
  CreateNodeTokenResponse,
  NodeTokenInfo,
  NotificationEvent,
  OperatorTokenInfo,
  ProviderTargetSummary,
  RouteSummary,
  TestSendResponse
} from '../types.js';

interface DashboardData {
  tokenInfo: OperatorTokenInfo;
  nodeTokens: NodeTokenInfo[];
  targets: ProviderTargetSummary[];
  routes: RouteSummary[];
  captureMessages: CaptureMessage[];
}

interface DashboardHandlers {
  onLogout: () => Promise<void>;
  onRotateGenerate: (currentToken: string) => Promise<void>;
  onRotateCustom: (currentToken: string, token: string) => Promise<void>;
  onCreateNodeToken: (sourceService: string, sourceInstance: string) => Promise<CreateNodeTokenResponse>;
  onDeleteNodeToken: (keyId: string) => Promise<void>;
  onSaveTarget: (payload: Record<string, unknown>, targetId: string | null) => Promise<void>;
  onDeleteTarget: (targetId: string) => Promise<void>;
  onStartEditTarget: (targetId: string | null) => void;
  onSaveRoute: (payload: Record<string, unknown>, routeId: string | null) => Promise<void>;
  onDeleteRoute: (routeId: string) => Promise<void>;
  onStartEditRoute: (routeId: string | null) => void;
  onTestSend: (event: NotificationEvent, targetIds: string[]) => Promise<TestSendResponse>;
  onClearCapture: () => Promise<void>;
  onRefresh: () => Promise<void>;
}

function escapeHtml(value: string): string {
  return value
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

function formatTimestamp(value: number | string | null): string {
  if (value === null) {
    return 'never';
  }
  if (typeof value === 'string') {
    return value;
  }
  return new Date(value).toLocaleString();
}

function renderSummary(summary: Record<string, string | number | boolean | null>): string {
  const entries = Object.entries(summary);
  if (entries.length === 0) {
    return '<span class="subtle">No credentials shown</span>';
  }
  return entries.map(([key, value]) => `<span>${escapeHtml(key)}=${escapeHtml(String(value ?? ''))}</span>`).join('');
}

function sampleEvent(): NotificationEvent {
  return {
    schema_version: 1,
    event_name: 'demo.test',
    severity: 'info',
    timestamp: new Date().toISOString(),
    source: {
      service: 'manual',
      instance: 'dashboard'
    },
    attributes: {
      note: 'hello from fbnotify',
      route: 'ui-test'
    }
  };
}

function buildTargetForm(editingTarget: ProviderTargetSummary | undefined): string {
  const type = editingTarget?.type ?? 'webhook';
  return `
    <form id="target-form" class="stack">
      <div class="split">
        <div class="field">
          <label for="target-name">Name</label>
          <input id="target-name" class="text-input" value="${escapeHtml(editingTarget?.name ?? '')}" placeholder="Primary webhook">
        </div>
        <div class="field">
          <label for="target-type">Type</label>
          <select id="target-type" class="select-input" ${editingTarget ? 'disabled' : ''}>
            <option value="webhook" ${type === 'webhook' ? 'selected' : ''}>Webhook</option>
            <option value="pushover" ${type === 'pushover' ? 'selected' : ''}>Pushover</option>
            <option value="capture" ${type === 'capture' ? 'selected' : ''}>Capture</option>
          </select>
        </div>
      </div>
      <div id="target-config"></div>
      <div class="button-row">
        <button class="button" type="submit">${editingTarget ? 'Update target' : 'Create target'}</button>
        ${editingTarget ? '<button class="button secondary" id="cancel-target-edit" type="button">Cancel edit</button>' : ''}
      </div>
    </form>
  `;
}

function buildRouteForm(editingRoute: RouteSummary | undefined, targets: ProviderTargetSummary[]): string {
  const selected = new Set(editingRoute?.target_ids ?? []);
  return `
    <form id="route-form" class="stack">
      <div class="split">
        <div class="field">
          <label for="route-name">Name</label>
          <input id="route-name" class="text-input" value="${escapeHtml(editingRoute?.name ?? '')}" placeholder="Default route">
        </div>
        <div class="field">
          <label for="route-event">Event name</label>
          <input id="route-event" class="text-input mono" value="${escapeHtml(editingRoute?.event_name ?? '')}" placeholder="Leave blank for default">
        </div>
      </div>
      <div class="field">
        <label for="route-service">Source service</label>
        <input id="route-service" class="text-input mono" value="${escapeHtml(editingRoute?.source_service ?? '')}" placeholder="Leave blank for all services">
      </div>
      <div class="field">
        <label>Targets</label>
        <div class="checkbox-list">
          ${targets.length === 0
            ? '<div class="empty">Create at least one provider target before adding routes.</div>'
            : targets.map(target => `
              <label class="checkbox-row">
                <input type="checkbox" name="route-target" value="${escapeHtml(target.id)}" ${selected.has(target.id) ? 'checked' : ''}>
                <span><strong>${escapeHtml(target.name)}</strong> <span class="subtle">(${escapeHtml(target.type)})</span></span>
              </label>
            `).join('')}
        </div>
      </div>
      <div class="button-row">
        <button class="button" type="submit">${editingRoute ? 'Update route' : 'Create route'}</button>
        ${editingRoute ? '<button class="button secondary" id="cancel-route-edit" type="button">Cancel edit</button>' : ''}
      </div>
    </form>
  `;
}

export function renderDashboardPage(data: DashboardData, handlers: DashboardHandlers): HTMLElement {
  const shell = document.createElement('main');
  const editingTarget = data.targets.find(target => target.id === appState.editingTargetId);
  const editingRoute = data.routes.find(route => route.id === appState.editingRouteId);
  shell.className = 'shell';
  shell.innerHTML = `
    <section class="hero">
      <div>
        <div class="pill-row">
          <span class="pill">Standalone Control Plane</span>
          <span class="pill">Targets: ${data.targets.length}</span>
          <span class="pill">Routes: ${data.routes.length}</span>
          <span class="pill">Node tokens: ${data.nodeTokens.length}</span>
        </div>
        <h1>fbnotify</h1>
        <p>Manage provider targets, routing, emitter credentials, and operator access. Test delivery through the same Worker APIs used by future emitters.</p>
      </div>
      <div class="button-row">
        <button class="button secondary" id="refresh-page" type="button">Refresh</button>
        <button class="button secondary" id="logout-button" type="button">Log out</button>
      </div>
    </section>

    <section class="grid">
      <article class="panel">
        <div class="panel-header">
          <h2>Provider Targets</h2>
          <p>Create webhook, Pushover, or capture endpoints. Stored credentials are never shown after write.</p>
        </div>
        <div class="panel-body stack">
          ${buildTargetForm(editingTarget)}
          <div class="table-list">
            ${data.targets.length === 0
              ? '<div class="empty">No targets configured yet.</div>'
              : data.targets.map(target => `
                <div class="item">
                  <div class="item-header">
                    <div>
                      <div class="item-title">${escapeHtml(target.name)}</div>
                      <div class="meta">
                        <span>${escapeHtml(target.type)}</span>
                        <span>updated ${escapeHtml(formatTimestamp(target.updated_at))}</span>
                      </div>
                    </div>
                    <div class="button-row">
                      <button class="button ghost target-edit" data-target-id="${escapeHtml(target.id)}" type="button">Edit</button>
                      <button class="button danger target-delete" data-target-id="${escapeHtml(target.id)}" type="button">Delete</button>
                    </div>
                  </div>
                  <div class="meta">${renderSummary(target.summary)}</div>
                </div>
              `).join('')}
          </div>
        </div>
      </article>

      <article class="panel">
        <div class="panel-header">
          <h2>Routing</h2>
          <p>Specific service and event matches outrank broader defaults.</p>
        </div>
        <div class="panel-body stack">
          ${buildRouteForm(editingRoute, data.targets)}
          <div class="table-list">
            ${data.routes.length === 0
              ? '<div class="empty">No routes configured yet.</div>'
              : data.routes.map(route => `
                <div class="item">
                  <div class="item-header">
                    <div>
                      <div class="item-title">${escapeHtml(route.name)}</div>
                      <div class="meta">
                        <span>${escapeHtml(route.match_kind)}</span>
                        <span>targets=${route.target_ids.length}</span>
                      </div>
                    </div>
                    <div class="button-row">
                      <button class="button ghost route-edit" data-route-id="${escapeHtml(route.id)}" type="button">Edit</button>
                      <button class="button danger route-delete" data-route-id="${escapeHtml(route.id)}" type="button">Delete</button>
                    </div>
                  </div>
                  <div class="meta">
                    <span>service=${escapeHtml(route.source_service ?? '*')}</span>
                    <span>event=${escapeHtml(route.event_name ?? '*')}</span>
                  </div>
                  <p class="subtle mono">${escapeHtml(route.target_ids.join(', '))}</p>
                </div>
              `).join('')}
          </div>
        </div>
      </article>

      <article class="panel">
        <div class="panel-header">
          <h2>Emitter Node Tokens</h2>
          <p>One active token per source tuple. The raw token is shown only once on creation.</p>
        </div>
        <div class="panel-body stack">
          <form id="node-token-form" class="stack">
            <div class="split">
              <div class="field">
                <label for="source-service">Source service</label>
                <input id="source-service" class="text-input mono" placeholder="fbforward">
              </div>
              <div class="field">
                <label for="source-instance">Source instance</label>
                <input id="source-instance" class="text-input mono" placeholder="node-1">
              </div>
            </div>
            <div class="button-row">
              <button class="button" type="submit">Create node token</button>
            </div>
          </form>
          ${appState.generatedNodeToken ? `
            <div class="stack">
              <div class="subtle">Newest node token</div>
              <div class="code-box">${escapeHtml(appState.generatedNodeToken.key_id)} :: ${escapeHtml(appState.generatedNodeToken.token)}</div>
            </div>
          ` : ''}
          <div class="table-list">
            ${data.nodeTokens.length === 0
              ? '<div class="empty">No node tokens minted yet.</div>'
              : data.nodeTokens.map(token => `
                <div class="item">
                  <div class="item-header">
                    <div>
                      <div class="item-title mono">${escapeHtml(token.source_service)} / ${escapeHtml(token.source_instance)}</div>
                      <div class="meta">
                        <span>${escapeHtml(token.masked_prefix)}</span>
                        <span>key=${escapeHtml(token.key_id)}</span>
                      </div>
                    </div>
                    <button class="button danger node-token-delete" data-key-id="${escapeHtml(token.key_id)}" type="button">Revoke</button>
                  </div>
                  <p class="subtle">Created ${escapeHtml(formatTimestamp(token.created_at))}, last used ${escapeHtml(formatTimestamp(token.last_used_at))}</p>
                </div>
              `).join('')}
          </div>
        </div>
      </article>

      <article class="panel">
        <div class="panel-header">
          <h2>Operator Token</h2>
          <p>Current sessions stay valid after rotation. New logins must use the replacement token.</p>
        </div>
        <div class="panel-body stack">
          <div class="meta">
            <span>${escapeHtml(data.tokenInfo.masked_prefix)}</span>
            <span>created ${escapeHtml(formatTimestamp(data.tokenInfo.created_at))}</span>
          </div>
          <form id="rotate-generated-form" class="stack">
            <div class="field">
              <label for="current-operator-token">Current operator token</label>
              <input id="current-operator-token" class="text-input mono" type="password" placeholder="Required to rotate">
            </div>
            <div class="button-row">
              <button class="button" type="submit">Generate replacement token</button>
            </div>
          </form>
          <form id="rotate-custom-form" class="stack">
            <div class="field">
              <label for="custom-operator-token">Custom replacement token</label>
              <input id="custom-operator-token" class="text-input mono" placeholder="Leave blank to skip custom rotation">
            </div>
            <div class="button-row">
              <button class="button secondary" type="submit">Apply custom token</button>
            </div>
          </form>
          ${appState.generatedToken ? `
            <div class="stack">
              <div class="subtle">Newest generated operator token</div>
              <div class="code-box">${escapeHtml(appState.generatedToken)}</div>
            </div>
          ` : ''}
        </div>
      </article>

      <article class="panel">
        <div class="panel-header">
          <h2>Provider Test Send</h2>
          <p>Send a sample event through the selected targets, or leave the list empty to use route resolution.</p>
        </div>
        <div class="panel-body stack">
          <form id="test-send-form" class="stack">
            <div class="field">
              <label for="test-event-json">Sample event JSON</label>
              <textarea id="test-event-json" class="text-area">${escapeHtml(JSON.stringify(sampleEvent(), null, 2))}</textarea>
            </div>
            <div class="field">
              <label>Optional target override</label>
              <div class="checkbox-list">
                ${data.targets.length === 0
                  ? '<div class="empty">Create at least one target to send a test message.</div>'
                  : data.targets.map(target => `
                    <label class="checkbox-row">
                      <input type="checkbox" name="test-target" value="${escapeHtml(target.id)}">
                      <span><strong>${escapeHtml(target.name)}</strong> <span class="subtle">(${escapeHtml(target.type)})</span></span>
                    </label>
                  `).join('')}
              </div>
            </div>
            <div class="button-row">
              <button class="button" type="submit">Send test event</button>
            </div>
          </form>
          <div id="test-send-result" class="empty">No test send has been executed in this session.</div>
        </div>
      </article>

      <article class="panel">
        <div class="panel-header">
          <h2>Capture Inbox</h2>
          <p>The built-in capture target is the deterministic test oracle for this service.</p>
        </div>
        <div class="panel-body stack">
          <div class="button-row">
            <button class="button secondary" id="clear-capture" type="button">Clear inbox</button>
          </div>
          <div class="table-list">
            ${data.captureMessages.length === 0
              ? '<div class="empty">No captured messages yet.</div>'
              : data.captureMessages.map(message => `
                <div class="item">
                  <div class="item-header">
                    <div>
                      <div class="item-title">${escapeHtml(message.target_name)}</div>
                      <div class="meta">
                        <span class="tag ${escapeHtml(message.severity)}">${escapeHtml(message.severity)}</span>
                        <span>${escapeHtml(message.event_name)}</span>
                        <span>${escapeHtml(formatTimestamp(message.received_at))}</span>
                      </div>
                    </div>
                  </div>
                  <p class="subtle mono">${escapeHtml(message.source_service)} / ${escapeHtml(message.source_instance)}</p>
                  <div class="code-box">${escapeHtml(message.payload)}</div>
                </div>
              `).join('')}
          </div>
        </div>
      </article>
    </section>
  `;

  const logoutButton = shell.querySelector<HTMLButtonElement>('#logout-button');
  const refreshButton = shell.querySelector<HTMLButtonElement>('#refresh-page');
  const targetForm = shell.querySelector<HTMLFormElement>('#target-form');
  const routeForm = shell.querySelector<HTMLFormElement>('#route-form');
  const nodeTokenForm = shell.querySelector<HTMLFormElement>('#node-token-form');
  const rotateGeneratedForm = shell.querySelector<HTMLFormElement>('#rotate-generated-form');
  const rotateCustomForm = shell.querySelector<HTMLFormElement>('#rotate-custom-form');
  const testSendForm = shell.querySelector<HTMLFormElement>('#test-send-form');
  const clearCaptureButton = shell.querySelector<HTMLButtonElement>('#clear-capture');
  const targetConfigHost = shell.querySelector<HTMLElement>('#target-config');
  const targetTypeSelect = shell.querySelector<HTMLSelectElement>('#target-type');
  const testResult = shell.querySelector<HTMLElement>('#test-send-result');

  if (!logoutButton || !refreshButton || !targetForm || !routeForm || !nodeTokenForm || !rotateGeneratedForm || !rotateCustomForm || !testSendForm || !clearCaptureButton || !targetConfigHost || !targetTypeSelect || !testResult) {
    throw new Error('dashboard page failed to initialize');
  }

  const renderTargetConfigFields = (): void => {
    const selectedType = editingTarget?.type ?? targetTypeSelect.value;
    const fields = selectedType === 'webhook'
      ? `
        <div class="field">
          <label for="target-url">Webhook URL ${editingTarget ? '<span class="subtle">(leave blank to keep current)</span>' : ''}</label>
          <input id="target-url" class="text-input mono" placeholder="https://example.com/hook">
        </div>
      `
      : selectedType === 'pushover'
        ? `
          <div class="split">
            <div class="field">
              <label for="target-user-key">Pushover user key ${editingTarget ? '<span class="subtle">(leave blank to keep current)</span>' : ''}</label>
              <input id="target-user-key" class="text-input mono" placeholder="user key">
            </div>
            <div class="field">
              <label for="target-api-token">Pushover app token ${editingTarget ? '<span class="subtle">(leave blank to keep current)</span>' : ''}</label>
              <input id="target-api-token" class="text-input mono" placeholder="application token">
            </div>
          </div>
          <div class="field">
            <label for="target-device">Device override</label>
            <input id="target-device" class="text-input" placeholder="Optional device name">
          </div>
        `
        : '<div class="empty">Capture targets store notifications inside fbnotify for deterministic testing.</div>';
    targetConfigHost.innerHTML = fields;
  };

  renderTargetConfigFields();
  targetTypeSelect.addEventListener('change', renderTargetConfigFields);

  logoutButton.addEventListener('click', () => {
    void handlers.onLogout();
  });
  refreshButton.addEventListener('click', () => {
    void handlers.onRefresh();
  });

  targetForm.addEventListener('submit', event => {
    event.preventDefault();
    const name = shell.querySelector<HTMLInputElement>('#target-name')?.value.trim() ?? '';
    const selectedType = editingTarget?.type ?? targetTypeSelect.value;
    const payload: Record<string, unknown> = {
      name,
      ...(editingTarget ? {} : { type: selectedType })
    };
    if (selectedType === 'webhook') {
      const url = shell.querySelector<HTMLInputElement>('#target-url')?.value.trim() ?? '';
      payload.config = url ? { url } : {};
    } else if (selectedType === 'pushover') {
      const apiToken = shell.querySelector<HTMLInputElement>('#target-api-token')?.value.trim() ?? '';
      const userKey = shell.querySelector<HTMLInputElement>('#target-user-key')?.value.trim() ?? '';
      const device = shell.querySelector<HTMLInputElement>('#target-device')?.value.trim() ?? '';
      payload.config = {
        ...(apiToken ? { api_token: apiToken } : {}),
        ...(userKey ? { user_key: userKey } : {}),
        ...(device ? { device } : {})
      };
    } else {
      payload.config = {};
    }
    void handlers.onSaveTarget(payload, editingTarget?.id ?? null);
  });

  shell.querySelector<HTMLButtonElement>('#cancel-target-edit')?.addEventListener('click', () => {
    handlers.onStartEditTarget(null);
  });

  for (const button of shell.querySelectorAll<HTMLButtonElement>('.target-edit')) {
    button.addEventListener('click', () => {
      handlers.onStartEditTarget(button.dataset.targetId ?? null);
    });
  }

  for (const button of shell.querySelectorAll<HTMLButtonElement>('.target-delete')) {
    button.addEventListener('click', () => {
      const targetId = button.dataset.targetId ?? '';
      if (!window.confirm('Delete this provider target? Any routes still pointing to it must be removed first.')) {
        return;
      }
      void handlers.onDeleteTarget(targetId);
    });
  }

  routeForm.addEventListener('submit', event => {
    event.preventDefault();
    const targetIds = Array.from(shell.querySelectorAll<HTMLInputElement>('input[name="route-target"]:checked'))
      .map(input => input.value);
    const payload = {
      name: shell.querySelector<HTMLInputElement>('#route-name')?.value.trim() ?? '',
      event_name: shell.querySelector<HTMLInputElement>('#route-event')?.value.trim() ?? '',
      source_service: shell.querySelector<HTMLInputElement>('#route-service')?.value.trim() ?? '',
      target_ids: targetIds
    };
    void handlers.onSaveRoute(payload, editingRoute?.id ?? null);
  });

  shell.querySelector<HTMLButtonElement>('#cancel-route-edit')?.addEventListener('click', () => {
    handlers.onStartEditRoute(null);
  });

  for (const button of shell.querySelectorAll<HTMLButtonElement>('.route-edit')) {
    button.addEventListener('click', () => {
      handlers.onStartEditRoute(button.dataset.routeId ?? null);
    });
  }

  for (const button of shell.querySelectorAll<HTMLButtonElement>('.route-delete')) {
    button.addEventListener('click', () => {
      const routeId = button.dataset.routeId ?? '';
      if (!window.confirm('Delete this route?')) {
        return;
      }
      void handlers.onDeleteRoute(routeId);
    });
  }

  nodeTokenForm.addEventListener('submit', event => {
    event.preventDefault();
    const sourceService = shell.querySelector<HTMLInputElement>('#source-service')?.value.trim() ?? '';
    const sourceInstance = shell.querySelector<HTMLInputElement>('#source-instance')?.value.trim() ?? '';
    void handlers.onCreateNodeToken(sourceService, sourceInstance);
  });

  for (const button of shell.querySelectorAll<HTMLButtonElement>('.node-token-delete')) {
    button.addEventListener('click', () => {
      const keyId = button.dataset.keyId ?? '';
      if (!window.confirm('Revoke this node token?')) {
        return;
      }
      void handlers.onDeleteNodeToken(keyId);
    });
  }

  rotateGeneratedForm.addEventListener('submit', event => {
    event.preventDefault();
    const currentToken = shell.querySelector<HTMLInputElement>('#current-operator-token')?.value.trim() ?? '';
    void handlers.onRotateGenerate(currentToken);
  });

  rotateCustomForm.addEventListener('submit', event => {
    event.preventDefault();
    const currentToken = shell.querySelector<HTMLInputElement>('#current-operator-token')?.value.trim() ?? '';
    const replacement = shell.querySelector<HTMLInputElement>('#custom-operator-token')?.value.trim() ?? '';
    void handlers.onRotateCustom(currentToken, replacement);
  });

  testSendForm.addEventListener('submit', event => {
    event.preventDefault();
    const raw = shell.querySelector<HTMLTextAreaElement>('#test-event-json')?.value ?? '';
    let parsed: NotificationEvent;
    try {
      parsed = JSON.parse(raw) as NotificationEvent;
    } catch {
      window.alert('Sample event JSON is invalid.');
      return;
    }
    const selectedTargets = Array.from(shell.querySelectorAll<HTMLInputElement>('input[name="test-target"]:checked')).map(input => input.value);
    void handlers.onTestSend(parsed, selectedTargets)
      .then(result => {
        testResult.className = 'code-box';
        testResult.textContent = JSON.stringify(result, null, 2);
      })
      .catch(error => {
        testResult.className = 'code-box';
        testResult.textContent = error instanceof Error ? error.message : 'test send failed';
      });
  });

  clearCaptureButton.addEventListener('click', () => {
    if (!window.confirm('Clear the built-in capture inbox?')) {
      return;
    }
    void handlers.onClearCapture();
  });

  return shell;
}
