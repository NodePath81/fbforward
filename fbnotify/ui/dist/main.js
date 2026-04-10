import { ApiError, checkAuth, clearCaptureMessages, createNodeToken, createRoute, createTarget, deleteRoute, deleteTarget, listCaptureMessages, listNodeTokens, listRoutes, listTargets, login, logout, revokeNodeToken, rotateToken, testSend, updateRoute, updateTarget, getTokenInfo } from './api.js';
import { renderDashboardPage } from './pages/dashboard.js';
import { renderLoginPage } from './pages/login.js';
import { appState } from './state.js';
const root = document.getElementById('app');
if (!root) {
    throw new Error('missing app root');
}
const app = root;
function parseRoute() {
    return window.location.hash === '#/login' ? { kind: 'login' } : { kind: 'dashboard' };
}
function setContent(element) {
    app.replaceChildren(element);
}
function showError(error) {
    const message = error instanceof Error ? error.message : 'unknown error';
    window.alert(message);
}
async function resolveAuth(force = false) {
    if (!force && appState.authenticated !== null) {
        return appState.authenticated;
    }
    try {
        await checkAuth();
        appState.authenticated = true;
    }
    catch (error) {
        if (error instanceof ApiError && error.status === 401) {
            appState.authenticated = false;
            return false;
        }
        throw error;
    }
    return true;
}
async function refreshDashboard() {
    await renderRoute();
}
async function renderRoute() {
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
            onSubmit: async (token) => {
                await login(token);
                appState.authenticated = true;
                window.location.hash = '#/';
            }
        }));
        return;
    }
    const [tokenInfo, nodeTokens, targets, routes, captureMessages] = await Promise.all([
        getTokenInfo(),
        listNodeTokens(),
        listTargets(),
        listRoutes(),
        listCaptureMessages()
    ]);
    if (renderId !== appState.renderNonce) {
        return;
    }
    setContent(renderDashboardPage({
        tokenInfo,
        nodeTokens,
        targets,
        routes,
        captureMessages
    }, {
        onLogout: async () => {
            await logout();
            appState.authenticated = false;
            window.location.hash = '#/login';
        },
        onRotateGenerate: async (currentToken) => {
            const result = await rotateToken({ current_token: currentToken, generate: true });
            appState.generatedToken = result.token ?? null;
            await refreshDashboard();
        },
        onRotateCustom: async (currentToken, token) => {
            if (!token) {
                throw new Error('custom replacement token is required');
            }
            await rotateToken({ current_token: currentToken, token });
            appState.generatedToken = null;
            await refreshDashboard();
        },
        onCreateNodeToken: async (sourceService, sourceInstance) => {
            const result = await createNodeToken(sourceService, sourceInstance);
            appState.generatedNodeToken = {
                key_id: result.key_id,
                token: result.token,
                source_service: result.info.source_service,
                source_instance: result.info.source_instance
            };
            await refreshDashboard();
            return result;
        },
        onDeleteNodeToken: async (keyId) => {
            await revokeNodeToken(keyId);
            if (appState.generatedNodeToken?.key_id === keyId) {
                appState.generatedNodeToken = null;
            }
            await refreshDashboard();
        },
        onSaveTarget: async (payload, targetId) => {
            if (targetId) {
                await updateTarget(targetId, payload);
            }
            else {
                await createTarget(payload);
            }
            appState.editingTargetId = null;
            await refreshDashboard();
        },
        onDeleteTarget: async (targetId) => {
            await deleteTarget(targetId);
            if (appState.editingTargetId === targetId) {
                appState.editingTargetId = null;
            }
            await refreshDashboard();
        },
        onStartEditTarget: targetId => {
            appState.editingTargetId = targetId;
            void refreshDashboard();
        },
        onSaveRoute: async (payload, routeId) => {
            const normalized = {
                ...payload,
                source_service: payload.source_service ? payload.source_service : null,
                event_name: payload.event_name ? payload.event_name : null
            };
            if (routeId) {
                await updateRoute(routeId, normalized);
            }
            else {
                await createRoute(normalized);
            }
            appState.editingRouteId = null;
            await refreshDashboard();
        },
        onDeleteRoute: async (routeId) => {
            await deleteRoute(routeId);
            if (appState.editingRouteId === routeId) {
                appState.editingRouteId = null;
            }
            await refreshDashboard();
        },
        onStartEditRoute: routeId => {
            appState.editingRouteId = routeId;
            void refreshDashboard();
        },
        onTestSend: async (event, targetIds) => testSend(event, targetIds),
        onClearCapture: async () => {
            await clearCaptureMessages();
            await refreshDashboard();
        },
        onRefresh: refreshDashboard
    }));
}
window.addEventListener('hashchange', () => {
    void renderRoute().catch(showError);
});
function start() {
    void resolveAuth()
        .catch(showError)
        .finally(() => {
        if (!window.location.hash) {
            window.location.hash = appState.authenticated ? '#/' : '#/login';
        }
        void renderRoute().catch(showError);
    });
}
if (document.readyState === 'loading') {
    window.addEventListener('DOMContentLoaded', start, { once: true });
}
else {
    start();
}
