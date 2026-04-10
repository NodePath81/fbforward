export class ApiError extends Error {
    status;
    constructor(message, status) {
        super(message);
        this.status = status;
    }
}
async function request(path, init) {
    const headers = new Headers(init?.headers);
    if (init?.body && !headers.has('content-type')) {
        headers.set('content-type', 'application/json');
    }
    const response = await fetch(path, {
        credentials: 'same-origin',
        ...init,
        headers
    });
    let body = null;
    try {
        body = await response.clone().json();
    }
    catch {
        body = null;
    }
    if (!response.ok) {
        throw new ApiError(body?.error ?? response.statusText, response.status);
    }
    return response.json();
}
export async function checkAuth() {
    await request('/api/auth/check');
}
export async function login(token) {
    await request('/api/auth/login', {
        method: 'POST',
        body: JSON.stringify({ token })
    });
}
export async function logout() {
    await request('/api/auth/logout', {
        method: 'POST'
    });
}
export async function getTokenInfo() {
    return request('/api/token/info');
}
export async function rotateToken(payload) {
    return request('/api/token/rotate', {
        method: 'POST',
        body: JSON.stringify(payload)
    });
}
export async function listNodeTokens() {
    const response = await request('/api/node-tokens');
    return response.tokens;
}
export async function createNodeToken(sourceService, sourceInstance) {
    return request('/api/node-tokens', {
        method: 'POST',
        body: JSON.stringify({
            source_service: sourceService,
            source_instance: sourceInstance
        })
    });
}
export async function revokeNodeToken(keyId) {
    await request(`/api/node-tokens/${encodeURIComponent(keyId)}`, {
        method: 'DELETE'
    });
}
export async function listTargets() {
    const response = await request('/api/targets');
    return response.targets;
}
export async function createTarget(payload) {
    return request('/api/targets', {
        method: 'POST',
        body: JSON.stringify(payload)
    });
}
export async function updateTarget(id, payload) {
    return request(`/api/targets/${encodeURIComponent(id)}`, {
        method: 'PUT',
        body: JSON.stringify(payload)
    });
}
export async function deleteTarget(id) {
    await request(`/api/targets/${encodeURIComponent(id)}`, {
        method: 'DELETE'
    });
}
export async function listRoutes() {
    const response = await request('/api/routes');
    return response.routes;
}
export async function createRoute(payload) {
    return request('/api/routes', {
        method: 'POST',
        body: JSON.stringify(payload)
    });
}
export async function updateRoute(id, payload) {
    return request(`/api/routes/${encodeURIComponent(id)}`, {
        method: 'PUT',
        body: JSON.stringify(payload)
    });
}
export async function deleteRoute(id) {
    await request(`/api/routes/${encodeURIComponent(id)}`, {
        method: 'DELETE'
    });
}
export async function testSend(event, targetIds) {
    return request('/api/test-send', {
        method: 'POST',
        body: JSON.stringify({
            event,
            target_ids: targetIds
        })
    });
}
export async function listCaptureMessages() {
    const response = await request('/api/capture/messages');
    return response.messages;
}
export async function clearCaptureMessages() {
    await request('/api/capture/clear', {
        method: 'POST'
    });
}
