import type { NodeTokenInfo, NotifyConfigInfo, TokenInfo } from '../types.js';

function formatTimestamp(timestamp: number | null): string {
  if (timestamp === null) {
    return 'Never';
  }
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: 'medium',
    timeStyle: 'short'
  }).format(timestamp);
}

export function renderTokenPage(options: {
  info: TokenInfo;
  nodeTokens: NodeTokenInfo[];
  notifyConfig: NotifyConfigInfo;
  generatedToken: string | null;
  generatedNodeToken: { node_id: string; token: string } | null;
  onGenerate: (currentToken: string) => Promise<void>;
  onSubmitCustom: (currentToken: string, token: string) => Promise<void>;
  onCopyGenerated: () => Promise<void>;
  onCreateNodeToken: (nodeId: string) => Promise<void>;
  onCopyGeneratedNodeToken: () => Promise<void>;
  onRevokeNodeToken: (nodeId: string) => Promise<void>;
  onUpdateNotifyConfig: (payload: {
    endpoint: string;
    key_id: string;
    token: string;
    source_instance: string;
  }) => Promise<void>;
  onSendTestNotification: () => Promise<void>;
}): HTMLElement {
  const shell = document.createElement('main');
  shell.className = 'shell';

  const toolbar = document.createElement('section');
  toolbar.className = 'toolbar';

  const nav = document.createElement('div');
  nav.className = 'nav-links';
  const dashboardLink = document.createElement('a');
  dashboardLink.className = 'pill';
  dashboardLink.href = '#/';
  dashboardLink.textContent = 'Back to Dashboard';
  nav.append(dashboardLink);

  toolbar.append(nav);

  const grid = document.createElement('section');
  grid.className = 'grid';

  const currentPanel = document.createElement('div');
  currentPanel.className = 'panel';
  const currentHeader = document.createElement('div');
  currentHeader.className = 'panel-header';
  const headerKicker = document.createElement('div');
  headerKicker.className = 'kicker';
  headerKicker.textContent = 'Operator Token';
  const headerTitle = document.createElement('h2');
  headerTitle.textContent = options.info.masked_prefix;
  const headerMeta = document.createElement('p');
  headerMeta.className = 'muted';
  headerMeta.textContent = `Last updated ${formatTimestamp(options.info.created_at)}`;
  currentHeader.append(headerKicker, headerTitle, headerMeta);

  const currentBody = document.createElement('div');
  currentBody.className = 'panel-body';
  const bodyText = document.createElement('p');
  bodyText.className = 'muted';
  bodyText.textContent = 'The full operator token is never shown here. Rotating it does not affect any existing node tokens.';
  currentBody.append(bodyText);
  currentPanel.append(currentHeader, currentBody);

  const rotatePanel = document.createElement('div');
  rotatePanel.className = 'panel';

  const header = document.createElement('div');
  header.className = 'panel-header';
  header.innerHTML = `
    <div class="kicker">Rotate Operator Token</div>
    <h2>Replace the operator credential</h2>
    <p class="muted">You can mint a fresh random operator token or paste your own replacement. Re-enter the current operator token to confirm the change.</p>
  `;

  const body = document.createElement('div');
  body.className = 'panel-body stack';

  const currentTokenField = document.createElement('label');
  currentTokenField.className = 'stack';
  const currentTokenLabel = document.createElement('span');
  currentTokenLabel.className = 'field-label';
  currentTokenLabel.textContent = 'Current operator token';
  const currentTokenInput = document.createElement('input');
  currentTokenInput.className = 'text-input';
  currentTokenInput.type = 'password';
  currentTokenInput.placeholder = 'Re-enter the current operator token to confirm rotation';
  currentTokenField.append(currentTokenLabel, currentTokenInput);

  if (options.generatedToken) {
    const tokenOnce = document.createElement('div');
    tokenOnce.className = 'token-once';

    const title = document.createElement('div');
    title.className = 'status-good';
    title.textContent = 'New operator token generated. This is the only time it will be shown.';

    const tokenValue = document.createElement('input');
    tokenValue.className = 'text-input inline-code';
    tokenValue.readOnly = true;
    tokenValue.value = options.generatedToken;

    const copyButton = document.createElement('button');
    copyButton.className = 'button secondary';
    copyButton.type = 'button';
    copyButton.textContent = 'Copy operator token';
    copyButton.addEventListener('click', () => {
      void options.onCopyGenerated();
    });

    tokenOnce.append(title, tokenValue, copyButton);
    body.append(tokenOnce);
  }

  const notice = document.createElement('div');
  notice.className = 'notice';
  notice.hidden = true;

  const generateButton = document.createElement('button');
  generateButton.className = 'button warn';
  generateButton.type = 'button';
  generateButton.textContent = 'Generate random operator token';
  generateButton.addEventListener('click', () => {
    const currentToken = currentTokenInput.value.trim();
    if (!currentToken) {
      notice.hidden = false;
      notice.textContent = 'Enter the current operator token to confirm rotation.';
      return;
    }
    notice.hidden = true;
    void options.onGenerate(currentToken)
      .catch(error => {
        notice.hidden = false;
        notice.textContent = error instanceof Error ? error.message : 'Operator token rotation failed';
      });
  });

  const customForm = document.createElement('form');
  customForm.className = 'stack';

  const label = document.createElement('label');
  label.className = 'stack';
  const labelText = document.createElement('span');
  labelText.className = 'field-label';
  labelText.textContent = 'Custom operator token';
  const input = document.createElement('input');
  input.className = 'text-input';
  input.type = 'password';
  input.placeholder = 'Enter a strong replacement operator token';
  label.append(labelText, input);

  const submit = document.createElement('button');
  submit.className = 'button secondary';
  submit.type = 'submit';
  submit.textContent = 'Apply custom operator token';

  customForm.append(label, notice, submit);
  customForm.addEventListener('submit', event => {
    event.preventDefault();
    const currentToken = currentTokenInput.value.trim();
    const token = input.value.trim();
    if (!currentToken) {
      notice.hidden = false;
      notice.textContent = 'Enter the current operator token to confirm rotation.';
      return;
    }
    if (token.length < 32) {
      notice.hidden = false;
      notice.textContent = 'Custom operator tokens must be at least 32 characters.';
      return;
    }
    notice.hidden = true;
    void options.onSubmitCustom(currentToken, token)
      .catch(error => {
        notice.hidden = false;
        notice.textContent = error instanceof Error ? error.message : 'Operator token rotation failed';
      });
  });

  body.append(currentTokenField, generateButton, customForm);
  rotatePanel.append(header, body);

  const nodePanel = document.createElement('div');
  nodePanel.className = 'panel';
  const nodeHeader = document.createElement('div');
  nodeHeader.className = 'panel-header';
  nodeHeader.innerHTML = `
    <div class="kicker">Node Tokens</div>
    <h2>Provision node credentials</h2>
    <p class="muted">Each node token is bound to exactly one node ID and can be revoked independently.</p>
  `;

  const nodeBody = document.createElement('div');
  nodeBody.className = 'panel-body stack';

  if (options.generatedNodeToken) {
    const tokenOnce = document.createElement('div');
    tokenOnce.className = 'token-once';

    const title = document.createElement('div');
    title.className = 'status-good';
    title.textContent = `New node token for ${options.generatedNodeToken.node_id}. This is the only time it will be shown.`;

    const tokenValue = document.createElement('input');
    tokenValue.className = 'text-input inline-code';
    tokenValue.readOnly = true;
    tokenValue.value = options.generatedNodeToken.token;

    const copyButton = document.createElement('button');
    copyButton.className = 'button secondary';
    copyButton.type = 'button';
    copyButton.textContent = 'Copy node token';
    copyButton.addEventListener('click', () => {
      void options.onCopyGeneratedNodeToken();
    });

    tokenOnce.append(title, tokenValue, copyButton);
    nodeBody.append(tokenOnce);
  }

  const createNotice = document.createElement('div');
  createNotice.className = 'notice';
  createNotice.hidden = true;

  const createForm = document.createElement('form');
  createForm.className = 'stack';
  const nodeIdLabel = document.createElement('label');
  nodeIdLabel.className = 'stack';
  const nodeIdText = document.createElement('span');
  nodeIdText.className = 'field-label';
  nodeIdText.textContent = 'Node ID';
  const nodeIdInput = document.createElement('input');
  nodeIdInput.className = 'text-input';
  nodeIdInput.type = 'text';
  nodeIdInput.placeholder = 'Enter a unique node ID';
  nodeIdLabel.append(nodeIdText, nodeIdInput);

  const createButton = document.createElement('button');
  createButton.className = 'button';
  createButton.type = 'submit';
  createButton.textContent = 'Mint node token';

  createForm.append(nodeIdLabel, createNotice, createButton);
  createForm.addEventListener('submit', event => {
    event.preventDefault();
    const nodeId = nodeIdInput.value.trim();
    if (!nodeId) {
      createNotice.hidden = false;
      createNotice.textContent = 'Enter a node ID.';
      return;
    }
    createNotice.hidden = true;
    void options.onCreateNodeToken(nodeId)
      .catch(error => {
        createNotice.hidden = false;
        createNotice.textContent = error instanceof Error ? error.message : 'Node token creation failed';
      });
  });

  nodeBody.append(createForm);

  if (options.nodeTokens.length === 0) {
    const empty = document.createElement('div');
    empty.className = 'empty-state';
    empty.textContent = 'No node tokens have been provisioned yet.';
    nodeBody.append(empty);
  } else {
    const tableWrap = document.createElement('div');
    tableWrap.className = 'table-wrap';
    const table = document.createElement('table');
    const thead = document.createElement('thead');
    thead.innerHTML = `
      <tr>
        <th>Node ID</th>
        <th>Token Prefix</th>
        <th>Created</th>
        <th>Last Used</th>
        <th>Action</th>
      </tr>
    `;
    table.append(thead);

    const tbody = document.createElement('tbody');
    for (const token of options.nodeTokens) {
      const row = document.createElement('tr');

      const nodeId = document.createElement('td');
      nodeId.className = 'inline-code';
      nodeId.textContent = token.node_id;

      const prefix = document.createElement('td');
      prefix.className = 'inline-code';
      prefix.textContent = token.masked_prefix;

      const created = document.createElement('td');
      created.textContent = formatTimestamp(token.created_at);

      const lastUsed = document.createElement('td');
      lastUsed.textContent = formatTimestamp(token.last_used_at);

      const action = document.createElement('td');
      const revokeButton = document.createElement('button');
      revokeButton.className = 'button secondary';
      revokeButton.type = 'button';
      revokeButton.textContent = 'Revoke';
      revokeButton.addEventListener('click', () => {
        void options.onRevokeNodeToken(token.node_id)
          .catch(error => {
            createNotice.hidden = false;
            createNotice.textContent = error instanceof Error ? error.message : 'Node token revocation failed';
          });
      });
      action.append(revokeButton);

      row.append(nodeId, prefix, created, lastUsed, action);
      tbody.append(row);
    }
    table.append(tbody);
    tableWrap.append(table);
    nodeBody.append(tableWrap);
  }

  nodePanel.append(nodeHeader, nodeBody);

  const notifyPanel = document.createElement('div');
  notifyPanel.className = 'panel';
  const notifyHeader = document.createElement('div');
  notifyHeader.className = 'panel-header';
  notifyHeader.innerHTML = `
    <div class="kicker">fbnotify Delivery Config</div>
    <h2>Manage outbound notification delivery</h2>
    <p class="muted">This sender config controls how fbcoord signs and posts events to fbnotify. The token is write-only and is never shown after save.</p>
  `;

  const notifyBody = document.createElement('div');
  notifyBody.className = 'panel-body stack';

  const notifySummary = document.createElement('div');
  notifySummary.className = 'stack';
  const summaryStatus = document.createElement('div');
  summaryStatus.className = options.notifyConfig.configured ? 'status-good' : 'status-warn';
  summaryStatus.textContent = options.notifyConfig.configured
    ? `Configured via ${options.notifyConfig.source}`
    : `Not configured${options.notifyConfig.missing.length > 0 ? ` (${options.notifyConfig.missing.join(', ')} missing)` : ''}`;
  const summaryMeta = document.createElement('div');
  summaryMeta.className = 'muted';
  summaryMeta.textContent = [
    options.notifyConfig.endpoint ? `Endpoint ${options.notifyConfig.endpoint}` : null,
    options.notifyConfig.key_id ? `Key ID ${options.notifyConfig.key_id}` : null,
    options.notifyConfig.source_instance ? `Source instance ${options.notifyConfig.source_instance}` : null,
    options.notifyConfig.masked_prefix ? `Token ${options.notifyConfig.masked_prefix}` : null,
    options.notifyConfig.updated_at !== null ? `Updated ${formatTimestamp(options.notifyConfig.updated_at)}` : null
  ].filter(Boolean).join(' | ') || 'No sender config is currently stored.';
  notifySummary.append(summaryStatus, summaryMeta);

  const notifyNotice = document.createElement('div');
  notifyNotice.className = 'notice';
  notifyNotice.hidden = true;

  const notifyForm = document.createElement('form');
  notifyForm.className = 'stack';

  const endpointLabel = document.createElement('label');
  endpointLabel.className = 'stack';
  const endpointText = document.createElement('span');
  endpointText.className = 'field-label';
  endpointText.textContent = 'Endpoint';
  const endpointInput = document.createElement('input');
  endpointInput.className = 'text-input';
  endpointInput.type = 'url';
  endpointInput.placeholder = 'https://fbnotify.example/v1/events';
  endpointInput.value = options.notifyConfig.endpoint;
  endpointLabel.append(endpointText, endpointInput);

  const keyIdLabel = document.createElement('label');
  keyIdLabel.className = 'stack';
  const keyIdText = document.createElement('span');
  keyIdText.className = 'field-label';
  keyIdText.textContent = 'Key ID';
  const keyIdInput = document.createElement('input');
  keyIdInput.className = 'text-input';
  keyIdInput.type = 'text';
  keyIdInput.placeholder = 'fbcoord-key-id';
  keyIdInput.value = options.notifyConfig.key_id;
  keyIdLabel.append(keyIdText, keyIdInput);

  const sourceInstanceLabel = document.createElement('label');
  sourceInstanceLabel.className = 'stack';
  const sourceInstanceText = document.createElement('span');
  sourceInstanceText.className = 'field-label';
  sourceInstanceText.textContent = 'Source instance';
  const sourceInstanceInput = document.createElement('input');
  sourceInstanceInput.className = 'text-input';
  sourceInstanceInput.type = 'text';
  sourceInstanceInput.placeholder = 'fbcoord';
  sourceInstanceInput.value = options.notifyConfig.source_instance;
  sourceInstanceLabel.append(sourceInstanceText, sourceInstanceInput);

  const tokenLabel = document.createElement('label');
  tokenLabel.className = 'stack';
  const tokenText = document.createElement('span');
  tokenText.className = 'field-label';
  tokenText.textContent = 'Signing token';
  const tokenInput = document.createElement('input');
  tokenInput.className = 'text-input';
  tokenInput.type = 'password';
  tokenInput.placeholder = options.notifyConfig.configured ? 'Enter a replacement signing token' : 'Enter the fbnotify signing token';
  tokenLabel.append(tokenText, tokenInput);

  const notifySubmit = document.createElement('button');
  notifySubmit.className = 'button';
  notifySubmit.type = 'submit';
  notifySubmit.textContent = 'Save fbnotify config';

  const notifyTestButton = document.createElement('button');
  notifyTestButton.className = 'button secondary';
  notifyTestButton.type = 'button';
  notifyTestButton.textContent = 'Send Test Notification';
  notifyTestButton.addEventListener('click', () => {
    notifyNotice.hidden = true;
    void options.onSendTestNotification()
      .then(() => {
        notifyNotice.hidden = false;
        notifyNotice.textContent = 'Test notification queued.';
      })
      .catch(error => {
        notifyNotice.hidden = false;
        notifyNotice.textContent = error instanceof Error ? error.message : 'Test notification failed';
      });
  });

  const notifyActions = document.createElement('div');
  notifyActions.className = 'button-row';
  notifyActions.append(notifySubmit, notifyTestButton);

  notifyForm.append(endpointLabel, keyIdLabel, sourceInstanceLabel, tokenLabel, notifyNotice, notifyActions);
  notifyForm.addEventListener('submit', event => {
    event.preventDefault();
    const endpoint = endpointInput.value.trim();
    const keyId = keyIdInput.value.trim();
    const sourceInstance = sourceInstanceInput.value.trim();
    const token = tokenInput.value.trim();
    if (!endpoint || !keyId || !sourceInstance || !token) {
      notifyNotice.hidden = false;
      notifyNotice.textContent = 'Endpoint, key ID, source instance, and token are all required.';
      return;
    }
    notifyNotice.hidden = true;
    void options.onUpdateNotifyConfig({
      endpoint,
      key_id: keyId,
      token,
      source_instance: sourceInstance,
    }).catch(error => {
      notifyNotice.hidden = false;
      notifyNotice.textContent = error instanceof Error ? error.message : 'fbnotify config update failed';
    });
  });

  notifyBody.append(notifySummary, notifyForm);
  notifyPanel.append(notifyHeader, notifyBody);

  grid.append(currentPanel, rotatePanel, nodePanel, notifyPanel);
  shell.append(toolbar, grid);
  return shell;
}
