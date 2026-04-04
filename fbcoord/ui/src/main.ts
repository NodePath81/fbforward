import { ApiError, checkAuth, getPool, getPools, getTokenInfo, login, rotateToken } from './api.js';
import { renderDashboardPage } from './pages/dashboard.js';
import { renderLoginPage } from './pages/login.js';
import { renderPoolDetailPage } from './pages/pool-detail.js';
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
  | { kind: 'pool'; pool: string }
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
  if (hash.startsWith('#/pool/')) {
    return {
      kind: 'pool',
      pool: decodeURIComponent(hash.slice('#/pool/'.length))
    };
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
            throw new Error('Invalid token.');
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
    const pools = await getPools();
    if (renderId !== appState.renderNonce) {
      return;
    }
    setContent(renderDashboardPage({
      pools,
      pollIntervalMs: appState.pollIntervalMs,
      onPollIntervalChange: ms => {
        appState.pollIntervalMs = ms;
        void renderRoute();
      }
    }));
    schedulePolling();
    return;
  }

  if (route.kind === 'pool') {
    try {
      const detail = await getPool(route.pool);
      if (renderId !== appState.renderNonce) {
        return;
      }
      setContent(renderPoolDetailPage(detail));
      schedulePolling();
      return;
    } catch (error) {
      if (error instanceof ApiError && error.status === 404) {
        window.location.hash = '#/';
        return;
      }
      throw error;
    }
  }

  const info = await getTokenInfo();
  if (renderId !== appState.renderNonce) {
    return;
  }
  setContent(renderTokenPage({
    info,
    generatedToken: appState.generatedToken,
    onGenerate: async () => {
      if (!requiresConfirmation('Rotate the shared token now? Connected nodes using the old token will need to be updated.')) {
        return;
      }
      const result = await rotateToken({ generate: true });
      appState.generatedToken = result.token ?? null;
      await renderRoute();
    },
    onSubmitCustom: async token => {
      if (!requiresConfirmation('Replace the shared token with this custom value? Connected nodes using the old token will need to be updated.')) {
        return;
      }
      await rotateToken({ token });
      appState.generatedToken = null;
      await renderRoute();
    },
    onCopyGenerated: async () => {
      if (!appState.generatedToken) {
        return;
      }
      await navigator.clipboard.writeText(appState.generatedToken);
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
