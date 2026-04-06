import type { TokenInfo } from '../types.js';

function formatTimestamp(timestamp: number): string {
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: 'medium',
    timeStyle: 'short'
  }).format(timestamp);
}

export function renderTokenPage(options: {
  info: TokenInfo;
  generatedToken: string | null;
  onGenerate: (currentToken: string) => Promise<void>;
  onSubmitCustom: (currentToken: string, token: string) => Promise<void>;
  onCopyGenerated: () => Promise<void>;
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
  headerKicker.textContent = 'Current Token';
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
  bodyText.textContent = 'The full token is never shown here. After rotation, connected nodes must be updated before they can reconnect.';
  currentBody.append(bodyText);
  currentPanel.append(currentHeader, currentBody);

  const rotatePanel = document.createElement('div');
  rotatePanel.className = 'panel';

  const header = document.createElement('div');
  header.className = 'panel-header';
  header.innerHTML = `
    <div class="kicker">Rotate Token</div>
    <h2>Replace the shared credential</h2>
    <p class="muted">You can mint a fresh random token or paste your own replacement. Re-enter the current token to confirm the change.</p>
  `;

  const body = document.createElement('div');
  body.className = 'panel-body stack';

  const currentTokenField = document.createElement('label');
  currentTokenField.className = 'stack';
  const currentTokenLabel = document.createElement('span');
  currentTokenLabel.className = 'field-label';
  currentTokenLabel.textContent = 'Current token';
  const currentTokenInput = document.createElement('input');
  currentTokenInput.className = 'text-input';
  currentTokenInput.type = 'password';
  currentTokenInput.placeholder = 'Re-enter the current shared token to confirm rotation';
  currentTokenField.append(currentTokenLabel, currentTokenInput);

  if (options.generatedToken) {
    const tokenOnce = document.createElement('div');
    tokenOnce.className = 'token-once';

    const title = document.createElement('div');
    title.className = 'status-good';
    title.textContent = 'New token generated. This is the only time it will be shown.';

    const tokenValue = document.createElement('input');
    tokenValue.className = 'text-input inline-code';
    tokenValue.readOnly = true;
    tokenValue.value = options.generatedToken;

    const copyButton = document.createElement('button');
    copyButton.className = 'button secondary';
    copyButton.type = 'button';
    copyButton.textContent = 'Copy token';
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
  generateButton.textContent = 'Generate random token';
  generateButton.addEventListener('click', () => {
    const currentToken = currentTokenInput.value.trim();
    if (!currentToken) {
      notice.hidden = false;
      notice.textContent = 'Enter the current token to confirm rotation.';
      return;
    }
    notice.hidden = true;
    void options.onGenerate(currentToken)
      .catch(error => {
        notice.hidden = false;
        notice.textContent = error instanceof Error ? error.message : 'Token rotation failed';
      });
  });

  const customForm = document.createElement('form');
  customForm.className = 'stack';

  const label = document.createElement('label');
  label.className = 'stack';
  const labelText = document.createElement('span');
  labelText.className = 'field-label';
  labelText.textContent = 'Custom token';
  const input = document.createElement('input');
  input.className = 'text-input';
  input.type = 'password';
  input.placeholder = 'Enter a strong replacement token';
  label.append(labelText, input);

  const submit = document.createElement('button');
  submit.className = 'button secondary';
  submit.type = 'submit';
  submit.textContent = 'Apply custom token';

  customForm.append(label, notice, submit);
  customForm.addEventListener('submit', event => {
    event.preventDefault();
    const currentToken = currentTokenInput.value.trim();
    const token = input.value.trim();
    if (!currentToken) {
      notice.hidden = false;
      notice.textContent = 'Enter the current token to confirm rotation.';
      return;
    }
    if (token.length < 32) {
      notice.hidden = false;
      notice.textContent = 'Custom tokens must be at least 32 characters.';
      return;
    }
    notice.hidden = true;
    void options.onSubmitCustom(currentToken, token)
      .catch(error => {
        notice.hidden = false;
        notice.textContent = error instanceof Error ? error.message : 'Token rotation failed';
      });
  });

  body.append(currentTokenField, generateButton, customForm);
  rotatePanel.append(header, body);

  grid.append(currentPanel, rotatePanel);
  shell.append(toolbar, grid);
  return shell;
}
