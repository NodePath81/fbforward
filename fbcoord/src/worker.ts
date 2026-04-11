import { extractBearerToken, extractClientKey, getCookie, isAllowedOrigin } from './auth';
import { AuthGuardDurableObject, activeBanKey, manualDenyKey, type AuthScope, type GuardStatusResponse } from './durable-objects/auth-guard';
import { AUTHENTICATED_NODE_ID_HEADER, PoolDurableObject } from './durable-objects/pool';
import { RegistryDurableObject } from './durable-objects/registry';
import { TokenDurableObject } from './durable-objects/token';
import { createNotifier, resolveEnvNotifyConfig } from './notify';
import { clearSessionCookie, createSessionCookie, createSessionRecord, SESSION_COOKIE_NAME, validateSession } from './session';

export interface Env {
  FBCOORD_POOL: DurableObjectNamespace;
  FBCOORD_REGISTRY: DurableObjectNamespace;
  FBCOORD_TOKEN_STORE: DurableObjectNamespace;
  FBCOORD_AUTH_GUARD: DurableObjectNamespace;
  FBCOORD_AUTH_KV: KVNamespace;
  FBCOORD_TOKEN: string;
  FBCOORD_TOKEN_PEPPER: string;
  FBNOTIFY_URL?: string;
  FBNOTIFY_KEY_ID?: string;
  FBNOTIFY_TOKEN?: string;
  FBNOTIFY_SOURCE_INSTANCE?: string;
  ASSETS?: Fetcher;
}

const GLOBAL_OBJECT_NAME = 'global';

interface OperatorTokenValidationResponse {
  valid: boolean;
}

interface NodeTokenValidationResponse {
  valid: boolean;
  node_id?: string;
}

interface TokenInfoResponse {
  masked_prefix: string;
  created_at: number;
}

interface NodeTokenInfoResponse {
  node_id: string;
  masked_prefix: string;
  created_at: number;
  last_used_at: number | null;
}

interface SessionSecretResponse {
  session_secret: string;
}

interface StateResponse {
  pick: {
    version: number;
    upstream: string | null;
  };
  node_count: number;
  counts: {
    online: number;
    offline: number;
    aborted: number;
    never_seen: number;
  };
  nodes: Array<{
    node_id: string;
    status: 'online' | 'offline' | 'aborted' | 'never_seen';
    first_seen_at: number | null;
    last_connected_at: number | null;
    last_seen_at: number | null;
    disconnected_at: number | null;
    upstreams: string[];
    active_upstream: string | null;
  }>;
}

interface InternalStateResponse {
  pick: {
    version: number;
    upstream: string | null;
  };
  node_count: number;
  counts: {
    online: number;
    offline: number;
    aborted: number;
  };
  nodes: Array<{
    node_id: string;
    status: 'online' | 'offline' | 'aborted';
    first_seen_at: number;
    last_connected_at: number;
    last_seen_at: number;
    disconnected_at: number | null;
    upstreams: string[];
    active_upstream: string | null;
  }>;
}

interface RotateTokenResponse {
  info: TokenInfoResponse;
  token?: string;
}

interface NotifyConfigResponse {
  configured: boolean;
  source: 'stored' | 'bootstrap-env' | 'none';
  endpoint: string;
  key_id: string;
  source_instance: string;
  masked_prefix: string;
  updated_at: number | null;
  missing: string[];
}

interface InternalNotifyConfigResponse extends NotifyConfigResponse {
  token?: string;
}

interface CreateNodeTokenResponse {
  token: string;
  info: NodeTokenInfoResponse;
}

interface ListNodeTokensResponse {
  tokens: NodeTokenInfoResponse[];
}

interface BanMarker {
  blocked_until?: number;
}

interface ExecutionContextLike {
  waitUntil(promise: Promise<unknown>): void;
}

function json(data: unknown, status: number = 200, headers?: HeadersInit): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: {
      'content-type': 'application/json; charset=utf-8',
      ...headers
    }
  });
}

function methodNotAllowed(allow: string): Response {
  return new Response('method not allowed', {
    status: 405,
    headers: {
      Allow: allow
    }
  });
}

function tooManyRequests(retryAfterSeconds: number | null = null): Response {
  return json({ error: 'too many requests' }, 429, retryAfterSeconds
    ? { 'Retry-After': String(retryAfterSeconds) }
    : undefined);
}

function originMismatchResponse(): Response {
  return json({ error: 'forbidden' }, 403);
}

function secureCookiesFor(request: Request): boolean {
  return new URL(request.url).protocol === 'https:';
}

async function parseJsonBody<T>(request: Request): Promise<T | null> {
  try {
    return await request.json() as T;
  } catch {
    return null;
  }
}

async function parseJsonResponse<T>(response: Response): Promise<T | null> {
  try {
    return await response.json() as T;
  } catch {
    return null;
  }
}

function tokenStoreStub(env: Env): DurableObjectStub {
  return env.FBCOORD_TOKEN_STORE.get(env.FBCOORD_TOKEN_STORE.idFromName(GLOBAL_OBJECT_NAME));
}

function stateStub(env: Env): DurableObjectStub {
  return env.FBCOORD_POOL.get(env.FBCOORD_POOL.idFromName(GLOBAL_OBJECT_NAME));
}

function authGuardStub(env: Env, scope: AuthScope, clientKey: string): DurableObjectStub {
  return env.FBCOORD_AUTH_GUARD.get(env.FBCOORD_AUTH_GUARD.idFromName(`${scope}:${clientKey}`));
}

async function validateOperatorToken(env: Env, token: string): Promise<boolean> {
  const response = await tokenStoreStub(env).fetch(new Request('https://token.internal/validate-operator', {
    method: 'POST',
    headers: {
      'content-type': 'application/json'
    },
    body: JSON.stringify({ token })
  }));
  if (!response.ok) {
    return false;
  }
  const body = await response.json() as OperatorTokenValidationResponse;
  return body.valid;
}

async function validateNodeToken(env: Env, token: string): Promise<NodeTokenValidationResponse> {
  const response = await tokenStoreStub(env).fetch(new Request('https://token.internal/validate-node', {
    method: 'POST',
    headers: {
      'content-type': 'application/json'
    },
    body: JSON.stringify({ token })
  }));
  if (!response.ok) {
    return { valid: false };
  }
  return response.json() as Promise<NodeTokenValidationResponse>;
}

async function getSessionSecret(env: Env): Promise<string> {
  const response = await tokenStoreStub(env).fetch('https://token.internal/session-secret');
  const body = await response.json() as SessionSecretResponse;
  return body.session_secret;
}

async function getTokenInfo(env: Env): Promise<TokenInfoResponse> {
  const response = await tokenStoreStub(env).fetch('https://token.internal/info');
  return response.json() as Promise<TokenInfoResponse>;
}

async function rotateToken(env: Env, body: unknown): Promise<Response> {
  return tokenStoreStub(env).fetch(new Request('https://token.internal/rotate', {
    method: 'POST',
    headers: {
      'content-type': 'application/json'
    },
    body: JSON.stringify(body)
  }));
}

async function listNodeTokens(env: Env): Promise<NodeTokenInfoResponse[]> {
  const response = await tokenStoreStub(env).fetch('https://token.internal/node-tokens');
  const body = await response.json() as ListNodeTokensResponse;
  return body.tokens;
}

async function createNodeToken(env: Env, nodeId: string): Promise<Response> {
  return tokenStoreStub(env).fetch(new Request('https://token.internal/node-tokens', {
    method: 'POST',
    headers: {
      'content-type': 'application/json'
    },
    body: JSON.stringify({ node_id: nodeId })
  }));
}

async function revokeNodeToken(env: Env, nodeId: string): Promise<Response> {
  return tokenStoreStub(env).fetch(new Request(`https://token.internal/node-tokens/${encodeURIComponent(nodeId)}`, {
    method: 'DELETE'
  }));
}

async function fetchState(env: Env): Promise<StateResponse> {
  const response = await stateStub(env).fetch('https://pool.internal/state');
  const internal = await response.json() as InternalStateResponse;
  const provisioned = await listNodeTokens(env);

  const nodesById = new Map<string, StateResponse['nodes'][number]>();
  for (const node of internal.nodes) {
    nodesById.set(node.node_id, {
      node_id: node.node_id,
      status: node.status,
      first_seen_at: node.first_seen_at,
      last_connected_at: node.last_connected_at,
      last_seen_at: node.last_seen_at,
      disconnected_at: node.disconnected_at,
      upstreams: node.upstreams,
      active_upstream: node.active_upstream
    });
  }

  for (const token of provisioned) {
    if (nodesById.has(token.node_id)) {
      continue;
    }
    nodesById.set(token.node_id, {
      node_id: token.node_id,
      status: 'never_seen',
      first_seen_at: null,
      last_connected_at: null,
      last_seen_at: null,
      disconnected_at: null,
      upstreams: [],
      active_upstream: null
    });
  }

  const nodes = Array.from(nodesById.values()).sort((left, right) => left.node_id.localeCompare(right.node_id));
  const counts = {
    online: 0,
    offline: 0,
    aborted: 0,
    never_seen: 0
  };

  for (const node of nodes) {
    counts[node.status] += 1;
  }

  return {
    pick: internal.pick,
    node_count: counts.online,
    counts,
    nodes
  };
}

async function getNotifyConfig(env: Env): Promise<NotifyConfigResponse> {
  const response = await tokenStoreStub(env).fetch('https://token.internal/notify-config');
  return response.json() as Promise<NotifyConfigResponse>;
}

async function getInternalNotifyConfig(env: Env): Promise<InternalNotifyConfigResponse> {
  const response = await tokenStoreStub(env).fetch('https://token.internal/notify-config/internal');
  return response.json() as Promise<InternalNotifyConfigResponse>;
}

async function resolveWorkerNotifyConfig(env: Env): Promise<InternalNotifyConfigResponse> {
  const config = await getInternalNotifyConfig(env);
  if (config.configured) {
    return config;
  }
  return resolveEnvNotifyConfig(env);
}

async function updateNotifyConfig(env: Env, body: unknown): Promise<Response> {
  return tokenStoreStub(env).fetch(new Request('https://token.internal/notify-config', {
    method: 'PUT',
    headers: {
      'content-type': 'application/json'
    },
    body: JSON.stringify(body)
  }));
}

async function removeNodeFromState(env: Env, nodeId: string): Promise<Response> {
  return stateStub(env).fetch(new Request(`https://pool.internal/nodes/${encodeURIComponent(nodeId)}`, {
    method: 'DELETE'
  }));
}

async function checkKVBan(env: Env, scope: AuthScope, clientKey: string): Promise<{ blocked: boolean; retryAfterSeconds: number | null }> {
  const anyDeny = await env.FBCOORD_AUTH_KV.get(manualDenyKey('any', clientKey));
  if (anyDeny !== null) {
    return { blocked: true, retryAfterSeconds: null };
  }

  const scopedDeny = await env.FBCOORD_AUTH_KV.get(manualDenyKey(scope, clientKey));
  if (scopedDeny !== null) {
    return { blocked: true, retryAfterSeconds: null };
  }

  const activeBan = await env.FBCOORD_AUTH_KV.get<BanMarker>(activeBanKey(scope, clientKey), 'json');
  if (!activeBan?.blocked_until) {
    return { blocked: false, retryAfterSeconds: null };
  }

  const remainingMs = activeBan.blocked_until - Date.now();
  if (remainingMs <= 0) {
    return { blocked: false, retryAfterSeconds: null };
  }
  return {
    blocked: true,
    retryAfterSeconds: Math.max(1, Math.ceil(remainingMs / 1000))
  };
}

async function authGuardStatus(env: Env, scope: AuthScope, clientKey: string): Promise<GuardStatusResponse> {
  const response = await authGuardStub(env, scope, clientKey).fetch(new Request('https://auth-guard.internal/status', {
    method: 'POST',
    headers: {
      'content-type': 'application/json'
    },
    body: JSON.stringify({ scope, client_key: clientKey })
  }));
  return response.json() as Promise<GuardStatusResponse>;
}

async function recordAuthFailure(env: Env, scope: AuthScope, clientKey: string): Promise<void> {
  await authGuardStub(env, scope, clientKey).fetch(new Request('https://auth-guard.internal/failure', {
    method: 'POST',
    headers: {
      'content-type': 'application/json'
    },
    body: JSON.stringify({ scope, client_key: clientKey })
  }));
}

async function recordAuthSuccess(env: Env, scope: AuthScope, clientKey: string): Promise<void> {
  await authGuardStub(env, scope, clientKey).fetch(new Request('https://auth-guard.internal/success', {
    method: 'POST',
    headers: {
      'content-type': 'application/json'
    },
    body: JSON.stringify({ scope, client_key: clientKey })
  }));
}

async function enforceAuthGuard(env: Env, scope: AuthScope, clientKey: string): Promise<Response | null> {
  const kvStatus = await checkKVBan(env, scope, clientKey);
  if (kvStatus.blocked) {
    return tooManyRequests(kvStatus.retryAfterSeconds);
  }

  const status = await authGuardStatus(env, scope, clientKey);
  if (status.blocked) {
    return tooManyRequests(status.retry_after_seconds || null);
  }

  return null;
}

function requireSameOrigin(request: Request): Response | null {
  return isAllowedOrigin(request) ? null : originMismatchResponse();
}

function withAuthenticatedNodeId(request: Request, nodeId: string): Request {
  const headers = new Headers(request.headers);
  headers.set(AUTHENTICATED_NODE_ID_HEADER, nodeId);
  return new Request(request, { headers });
}

function noOpContext(): ExecutionContextLike {
  return {
    waitUntil(promise: Promise<unknown>): void {
      void promise;
    }
  };
}

function logNotificationTrigger(
  eventName: string,
  notifyConfig: NotifyConfigResponse,
  details: Record<string, unknown> = {}
): void {
  console.info('fbcoord notification trigger', {
    component: 'notify',
    service: 'fbcoord',
    event_name: eventName,
    notifier_enabled: notifyConfig.configured,
    notify_source: notifyConfig.source,
    notify_missing: notifyConfig.missing,
    ...details
  });
}

function createWorker() {
  return {
    async fetch(request: Request, env: Env, ctx: ExecutionContextLike = noOpContext()): Promise<Response> {
      const url = new URL(request.url);
      const clientKey = extractClientKey(request);
      const notifier = createNotifier(env, 'fbcoord', fetch, async () => await resolveWorkerNotifyConfig(env));

      if (url.pathname === '/healthz') {
        return new Response('ok', { status: 200 });
      }

      if (url.pathname === '/ws/node') {
        const preflight = await enforceAuthGuard(env, 'node-auth', clientKey);
        if (preflight) {
          return preflight;
        }

        const token = extractBearerToken(request);
        const validation = token ? await validateNodeToken(env, token) : { valid: false };
        if (!validation.valid || !validation.node_id) {
          await recordAuthFailure(env, 'node-auth', clientKey);
          return new Response('unauthorized', { status: 401 });
        }
        await recordAuthSuccess(env, 'node-auth', clientKey);

        return stateStub(env).fetch(withAuthenticatedNodeId(request, validation.node_id));
      }

      if (url.pathname === '/api/auth/login') {
        if (request.method !== 'POST') {
          return methodNotAllowed('POST');
        }
        const originError = requireSameOrigin(request);
        if (originError) {
          return originError;
        }

        const preflight = await enforceAuthGuard(env, 'login', clientKey);
        if (preflight) {
          return preflight;
        }

        const body = await parseJsonBody<{ token?: string }>(request);
        const token = body?.token?.trim();
        if (!token || !(await validateOperatorToken(env, token))) {
          await recordAuthFailure(env, 'login', clientKey);
          return json({ error: 'invalid token' }, 401);
        }
        await recordAuthSuccess(env, 'login', clientKey);

        const sessionSecret = await getSessionSecret(env);
        const session = await createSessionRecord(sessionSecret);
        return json({ ok: true }, 200, {
          'Set-Cookie': createSessionCookie(session.token, undefined, secureCookiesFor(request))
        });
      }

      if (url.pathname.startsWith('/api/')) {
        const cookie = getCookie(request, SESSION_COOKIE_NAME);
        if (!cookie) {
          return json({ error: 'unauthorized' }, 401);
        }

        const sessionSecret = await getSessionSecret(env);
        if (!(await validateSession(cookie, sessionSecret))) {
          return json({ error: 'unauthorized' }, 401);
        }

        if (url.pathname === '/api/auth/check') {
          if (request.method !== 'GET') {
            return methodNotAllowed('GET');
          }
          return json({ ok: true });
        }

        if (url.pathname === '/api/auth/logout') {
          if (request.method !== 'POST') {
            return methodNotAllowed('POST');
          }
          const originError = requireSameOrigin(request);
          if (originError) {
            return originError;
          }
          return json({ ok: true }, 200, {
            'Set-Cookie': clearSessionCookie(secureCookiesFor(request))
          });
        }

        if (url.pathname === '/api/state') {
          if (request.method !== 'GET') {
            return methodNotAllowed('GET');
          }
          return json(await fetchState(env));
        }

        if (url.pathname === '/api/token/info') {
          if (request.method !== 'GET') {
            return methodNotAllowed('GET');
          }
          return json(await getTokenInfo(env));
        }

        if (url.pathname === '/api/notify/config') {
          if (request.method === 'GET') {
            return json(await getNotifyConfig(env));
          }
          if (request.method === 'PUT') {
            const originError = requireSameOrigin(request);
            if (originError) {
              return originError;
            }
            const body = await parseJsonBody<{
              endpoint?: string;
              key_id?: string;
              token?: string;
              source_instance?: string;
            }>(request);
            if (!body) {
              return json({ error: 'invalid json' }, 400);
            }
            const response = await updateNotifyConfig(env, body);
            if (!response.ok) {
              const errorBody = await parseJsonResponse<{ error?: string }>(response.clone());
              return json({ error: errorBody?.error ?? 'invalid notify config' }, response.status);
            }
            return json(await response.json() as NotifyConfigResponse);
          }
          return methodNotAllowed('GET, PUT');
        }

        if (url.pathname === '/api/token/rotate') {
          if (request.method !== 'POST') {
            return methodNotAllowed('POST');
          }
          const originError = requireSameOrigin(request);
          if (originError) {
            return originError;
          }

          const body = await parseJsonBody<{ current_token?: string; token?: string; generate?: boolean }>(request);
          if (!body) {
            return json({ error: 'invalid json' }, 400);
          }

          const currentToken = body.current_token?.trim();
          if (!currentToken || !(await validateOperatorToken(env, currentToken))) {
            return json({ error: 'invalid current token' }, 401);
          }

          const response = await rotateToken(env, {
            token: body.token,
            generate: body.generate
          });
          if (!response.ok) {
            const errorBody = await parseJsonResponse<{ error?: string }>(response.clone());
            return json({ error: errorBody?.error ?? 'invalid token' }, response.status);
          }

          const result = await response.json() as RotateTokenResponse;
          const notifyConfig = await notifier.status();
          logNotificationTrigger('operator.token_rotated', notifyConfig);
          ctx.waitUntil(notifier.send('operator.token_rotated', 'warn'));
          return json({
            ...result.info,
            ...(result.token ? { token: result.token } : {})
          });
        }

        if (url.pathname === '/api/node-tokens') {
          if (request.method === 'GET') {
            return json({ tokens: await listNodeTokens(env) });
          }

          if (request.method === 'POST') {
            const originError = requireSameOrigin(request);
            if (originError) {
              return originError;
            }

            const body = await parseJsonBody<{ node_id?: string }>(request);
            if (!body) {
              return json({ error: 'invalid json' }, 400);
            }

            const response = await createNodeToken(env, body.node_id?.trim() ?? '');
            if (!response.ok) {
              const errorBody = await parseJsonResponse<{ error?: string }>(response.clone());
              return json({ error: errorBody?.error ?? 'invalid node token request' }, response.status);
            }
            return json(await response.json() as CreateNodeTokenResponse);
          }

          return methodNotAllowed('GET, POST');
        }

        if (url.pathname.startsWith('/api/node-tokens/')) {
          if (request.method !== 'DELETE') {
            return methodNotAllowed('DELETE');
          }
          const originError = requireSameOrigin(request);
          if (originError) {
            return originError;
          }

          const nodeId = decodeURIComponent(url.pathname.slice('/api/node-tokens/'.length)).trim();
          const response = await revokeNodeToken(env, nodeId);
          if (!response.ok) {
            const errorBody = await parseJsonResponse<{ error?: string }>(response.clone());
            return json({ error: errorBody?.error ?? 'invalid node token request' }, response.status);
          }
          const cleanup = await removeNodeFromState(env, nodeId);
          if (!cleanup.ok) {
            return json({ error: 'node token revoked but state cleanup failed' }, 500);
          }
          return json({ ok: true });
        }

        return json({ error: 'not found' }, 404);
      }

      if (env.ASSETS) {
        const assetUrl = new URL(request.url);
        if (assetUrl.pathname === '/') {
          assetUrl.pathname = '/index.html';
        }
        return env.ASSETS.fetch(new Request(assetUrl.toString(), request));
      }

      return new Response('not found', { status: 404 });
    }
  };
}

const worker = createWorker();

export default worker;
export { AuthGuardDurableObject, createWorker, PoolDurableObject, RegistryDurableObject, TokenDurableObject };
