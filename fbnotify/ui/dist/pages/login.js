function panel(content) {
    const element = document.createElement('main');
    element.className = 'login-shell';
    element.innerHTML = content;
    return element;
}
export function renderLoginPage(params) {
    const page = panel(`
    <section class="panel">
      <div class="panel-header">
        <span class="pill">Operator Access</span>
        <h1 style="margin: 12px 0 0;">fbnotify Admin</h1>
        <p>Use the operator token to manage provider targets, routing, node tokens, and test deliveries.</p>
      </div>
      <form class="panel-body stack" id="login-form">
        <div class="field">
          <label for="operator-token">Operator token</label>
          <input id="operator-token" class="text-input mono" type="password" placeholder="Paste the current operator token" autocomplete="current-password">
        </div>
        <div class="button-row">
          <button class="button" type="submit">Sign In</button>
        </div>
      </form>
    </section>
  `);
    const form = page.querySelector('#login-form');
    const input = page.querySelector('#operator-token');
    if (!form || !input) {
        throw new Error('login page failed to initialize');
    }
    form.addEventListener('submit', event => {
        event.preventDefault();
        void params.onSubmit(input.value.trim());
    });
    return page;
}
