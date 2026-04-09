import {
  ApiError,
  checkAuth,
  createNodeToken,
  getState,
  getTokenInfo,
  listNodeTokens,
  login,
  revokeNodeToken,
  rotateToken
} from './api.js';
import { renderDashboardPage } from './pages/dashboard.js';
import { renderLoginPage } from './pages/login.js';
import { renderTokenPage } from './pages/token.js';
import { appState } from './state.js';

const root = document.getElementById('app');
if (!root) {
  throw new Error('missing app root');
}
const app: HTMLElement = root;

let pollTimer: number | null = null;

type Route =
  | { kind: 'login' }
  | { kind: 'dashboard' }
  | { kind: 'token' };

function stopPolling(): void {
  if (pollTimer !== null) {
    window.clearTimeout(pollTimer);
    pollTimer = null;
  }
}

function schedulePolling(): void {
  stopPolling();
  if (document.hidden) {
    return;
  }
  pollTimer = window.setTimeout(() => {
    void renderRoute();
  }, appState.pollIntervalMs);
}

function parseRoute(): Route {
  const hash = window.location.hash || '#/';
  if (hash === '#/login') {
    return { kind: 'login' };
  }
  if (hash === '#/token') {
    return { kind: 'token' };
  }
  return { kind: 'dashboard' };
}

function setContent(element: HTMLElement): void {
  app.replaceChildren(element);
}

function requiresConfirmation(message: string): boolean {
  return window.confirm(message);
}

async function resolveAuth(force: boolean = false): Promise<boolean> {
  if (!force && appState.authenticated !== null) {
    return appState.authenticated;
  }
  try {
    await checkAuth();
    appState.authenticated = true;
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      appState.authenticated = false;
      return false;
    }
    throw error;
  }
  return true;
}

async function renderRoute(): Promise<void> {
  stopPolling();
  const renderId = ++appState.renderNonce;
  const route = parseRoute();

  if (route.kind !== 'login') {
    const authenticated = await resolveAuth();
    if (!authenticated) {
      window.location.hash = '#/login';
      return;
    }
  }

  if (route.kind === 'login') {
    setContent(renderLoginPage({
      onSubmit: async token => {
        try {
          await login(token);
        } catch (error) {
          if (error instanceof ApiError && error.status === 429) {
            const wait = error.retryAfterSeconds ? ` Try again in ${error.retryAfterSeconds}s.` : '';
            throw new Error(`Too many attempts.${wait}`);
          }
          if (error instanceof ApiError && error.status === 401) {
            throw new Error('Invalid operator token.');
          }
          throw error;
        }
        appState.authenticated = true;
        window.location.hash = '#/';
      }
    }));
    return;
  }

  if (route.kind === 'dashboard') {
    const state = await getState();
    if (renderId !== appState.renderNonce) {
      return;
    }
    setContent(renderDashboardPage({
      state,
      pollIntervalMs: appState.pollIntervalMs,
      onPollIntervalChange: ms => {
        appState.pollIntervalMs = ms;
        void renderRoute();
      }
    }));
    schedulePolling();
    return;
  }

  const [info, nodeTokens] = await Promise.all([
    getTokenInfo(),
    listNodeTokens()
  ]);
  if (renderId !== appState.renderNonce) {
    return;
  }
  setContent(renderTokenPage({
    info,
    nodeTokens,
    generatedToken: appState.generatedToken,
    generatedNodeToken: appState.generatedNodeToken,
    onGenerate: async currentToken => {
      if (!requiresConfirmation('Rotate the operator token now? Existing operator sessions stay valid, but new logins must use the replacement token.')) {
        return;
      }
      const result = await rotateToken({ current_token: currentToken, generate: true });
      appState.generatedToken = result.token ?? null;
      await renderRoute();
    },
    onSubmitCustom: async (currentToken, token) => {
      if (!requiresConfirmation('Replace the operator token with this custom value? Existing operator sessions stay valid, but new logins must use the replacement token.')) {
        return;
      }
      await rotateToken({ current_token: currentToken, token });
      appState.generatedToken = null;
      await renderRoute();
    },
    onCopyGenerated: async () => {
      if (!appState.generatedToken) {
        return;
      }
      await navigator.clipboard.writeText(appState.generatedToken);
    },
    onCreateNodeToken: async nodeId => {
      const result = await createNodeToken(nodeId);
      appState.generatedNodeToken = {
        node_id: result.info.node_id,
        token: result.token
      };
      await renderRoute();
    },
    onCopyGeneratedNodeToken: async () => {
      if (!appState.generatedNodeToken) {
        return;
      }
      await navigator.clipboard.writeText(appState.generatedNodeToken.token);
    },
    onRevokeNodeToken: async nodeId => {
      if (!requiresConfirmation(`Revoke the node token for ${nodeId}? Existing live connections are not forced closed, but future reconnects will fail.`)) {
        return;
      }
      await revokeNodeToken(nodeId);
      if (appState.generatedNodeToken?.node_id === nodeId) {
        appState.generatedNodeToken = null;
      }
      await renderRoute();
    }
  }));
}

window.addEventListener('hashchange', () => {
  void renderRoute();
});

document.addEventListener('visibilitychange', () => {
  if (!document.hidden) {
    void renderRoute();
  }
});

function start(): void {
  void resolveAuth()
    .catch(error => {
      console.error(error);
      appState.authenticated = false;
    })
    .finally(() => {
      if (!window.location.hash) {
        window.location.hash = appState.authenticated ? '#/' : '#/login';
      }
      void renderRoute().catch(error => {
        console.error(error);
        setContent(renderLoginPage({
          onSubmit: async token => {
            await login(token);
            appState.authenticated = true;
            window.location.hash = '#/';
          }
        }));
      });
    });
}

if (document.readyState === 'loading') {
  window.addEventListener('DOMContentLoaded', start, { once: true });
} else {
  start();
}
