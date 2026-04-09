export function renderLoginPage(options: {
  onSubmit: (token: string) => Promise<void>;
}): HTMLElement {
  const shell = document.createElement('main');
  shell.className = 'shell';

  const panel = document.createElement('section');
  panel.className = 'panel login-panel';

  const header = document.createElement('div');
  header.className = 'panel-header';
  header.innerHTML = `
    <div class="kicker">fbcoord</div>
    <h1>Operator Login</h1>
    <p class="muted">Use the operator token to open the admin dashboard and manage node credentials.</p>
  `;

  const body = document.createElement('div');
  body.className = 'panel-body stack';

  const label = document.createElement('label');
  label.className = 'stack';
  const labelText = document.createElement('span');
  labelText.className = 'field-label';
  labelText.textContent = 'Operator token';
  const input = document.createElement('input');
  input.className = 'text-input';
  input.type = 'password';
  input.name = 'token';
  input.autocomplete = 'current-password';
  input.placeholder = 'Paste the current operator token';
  label.append(labelText, input);

  const notice = document.createElement('div');
  notice.className = 'notice';
  notice.hidden = true;

  const buttonRow = document.createElement('div');
  buttonRow.className = 'button-row';
  const submit = document.createElement('button');
  submit.className = 'button';
  submit.type = 'submit';
  submit.textContent = 'Sign in';
  buttonRow.append(submit);

  const form = document.createElement('form');
  form.className = 'stack';
  form.append(label, notice, buttonRow);
  form.addEventListener('submit', event => {
    event.preventDefault();
    const token = input.value.trim();
    if (!token) {
      notice.hidden = false;
      notice.textContent = 'Enter the operator token.';
      return;
    }

    submit.disabled = true;
    notice.hidden = true;
    void options.onSubmit(token)
      .catch(error => {
        notice.hidden = false;
        notice.textContent = error instanceof Error ? error.message : 'Login failed';
      })
      .finally(() => {
        submit.disabled = false;
      });
  });

  body.append(form);
  panel.append(header, body);
  shell.append(panel);
  return shell;
}
