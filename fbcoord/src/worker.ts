import { extractBearerToken, extractClientKey, getCookie, isAllowedOrigin } from './auth';
import { AuthGuardDurableObject, activeBanKey, manualDenyKey, type AuthScope, type GuardStatusResponse } from './durable-objects/auth-guard';
import { RegistryDurableObject } from './durable-objects/registry';
import { PoolDurableObject } from './durable-objects/pool';
import { TokenDurableObject } from './durable-objects/token';
import { clearSessionCookie, createSession, createSessionCookie, SESSION_COOKIE_NAME, validateSession } from './session';
import { validatePoolName } from './validation';

export interface Env {
  FBCOORD_POOL: DurableObjectNamespace;
  FBCOORD_REGISTRY: DurableObjectNamespace;
  FBCOORD_TOKEN_STORE: DurableObjectNamespace;
  FBCOORD_AUTH_GUARD: DurableObjectNamespace;
  FBCOORD_AUTH_KV: KVNamespace;
  FBCOORD_TOKEN: string;
  FBCOORD_TOKEN_PEPPER: string;
  ASSETS?: Fetcher;
}

const GLOBAL_OBJECT_NAME = 'global';

interface TokenValidationResponse {
  valid: boolean;
}

interface TokenInfoResponse {
  masked_prefix: string;
  created_at: number;
}

interface SessionSecretResponse {
  session_secret: string;
}

interface RegistryListResponse {
  pools: string[];
}

interface PoolStateResponse {
  pool: string;
  pick: {
    version: number;
    upstream: string | null;
  };
  node_count: number;
  nodes: Array<{
    node_id: string;
    upstreams: string[];
    active_upstream: string | null;
    last_seen: number;
    connected_at: number;
  }>;
}

interface RotateTokenResponse {
  info: TokenInfoResponse;
  token?: string;
}

interface BanMarker {
  blocked_until?: number;
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

function registryStub(env: Env): DurableObjectStub {
  return env.FBCOORD_REGISTRY.get(env.FBCOORD_REGISTRY.idFromName(GLOBAL_OBJECT_NAME));
}

function authGuardStub(env: Env, scope: AuthScope, clientKey: string): DurableObjectStub {
  return env.FBCOORD_AUTH_GUARD.get(env.FBCOORD_AUTH_GUARD.idFromName(`${scope}:${clientKey}`));
}

async function validateSharedToken(env: Env, token: string): Promise<boolean> {
  const response = await tokenStoreStub(env).fetch(new Request('https://token.internal/validate', {
    method: 'POST',
    headers: {
      'content-type': 'application/json'
    },
    body: JSON.stringify({ token })
  }));
  if (!response.ok) {
    return false;
  }
  const body = await response.json() as TokenValidationResponse;
  return body.valid;
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

async function listPools(env: Env): Promise<string[]> {
  const response = await registryStub(env).fetch('https://registry.internal/list');
  const body = await response.json() as RegistryListResponse;
  return body.pools;
}

async function fetchPoolState(env: Env, pool: string): Promise<PoolStateResponse> {
  const durableObjectId = env.FBCOORD_POOL.idFromName(pool);
  const stub = env.FBCOORD_POOL.get(durableObjectId);
  const response = await stub.fetch(`https://pool.internal/state?pool=${encodeURIComponent(pool)}`);
  return response.json() as Promise<PoolStateResponse>;
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

function createWorker() {
  return {
    async fetch(request: Request, env: Env): Promise<Response> {
      const url = new URL(request.url);
      const clientKey = extractClientKey(request);

      if (url.pathname === '/healthz') {
        return new Response('ok', { status: 200 });
      }

      if (url.pathname === '/ws/node') {
        const preflight = await enforceAuthGuard(env, 'node-auth', clientKey);
        if (preflight) {
          return preflight;
        }

        const token = extractBearerToken(request);
        if (!token || !(await validateSharedToken(env, token))) {
          await recordAuthFailure(env, 'node-auth', clientKey);
          return new Response('unauthorized', { status: 401 });
        }
        await recordAuthSuccess(env, 'node-auth', clientKey);

        const pool = url.searchParams.get('pool')?.trim();
        if (!pool) {
          return new Response('missing pool', { status: 400 });
        }
        const poolError = validatePoolName(pool);
        if (poolError) {
          return new Response(poolError, { status: 400 });
        }

        const durableObjectId = env.FBCOORD_POOL.idFromName(pool);
        const stub = env.FBCOORD_POOL.get(durableObjectId);
        return stub.fetch(request);
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
        if (!token || !(await validateSharedToken(env, token))) {
          await recordAuthFailure(env, 'login', clientKey);
          return json({ error: 'invalid token' }, 401);
        }
        await recordAuthSuccess(env, 'login', clientKey);

        const sessionSecret = await getSessionSecret(env);
        const session = await createSession(sessionSecret);
        return json({ ok: true }, 200, {
          'Set-Cookie': createSessionCookie(session, undefined, secureCookiesFor(request))
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

        if (url.pathname === '/api/pools') {
          if (request.method !== 'GET') {
            return methodNotAllowed('GET');
          }

          const poolNames = await listPools(env);
          const pools = [];
          for (const pool of poolNames) {
            if (validatePoolName(pool)) {
              continue;
            }
            const state = await fetchPoolState(env, pool);
            if (state.node_count === 0) {
              continue;
            }
            pools.push({
              name: pool,
              node_count: state.node_count,
              pick: state.pick
            });
          }
          return json({ pools });
        }

        if (url.pathname.startsWith('/api/pools/')) {
          if (request.method !== 'GET') {
            return methodNotAllowed('GET');
          }

          const pool = decodeURIComponent(url.pathname.slice('/api/pools/'.length)).trim();
          if (!pool) {
            return json({ error: 'missing pool' }, 400);
          }
          const poolError = validatePoolName(pool);
          if (poolError) {
            return json({ error: poolError }, 400);
          }

          const state = await fetchPoolState(env, pool);
          if (state.node_count === 0) {
            return json({ error: 'pool not found' }, 404);
          }
          return json(state);
        }

        if (url.pathname === '/api/token/info') {
          if (request.method !== 'GET') {
            return methodNotAllowed('GET');
          }
          return json(await getTokenInfo(env));
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
          if (!currentToken || !(await validateSharedToken(env, currentToken))) {
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
          return json({
            ...result.info,
            ...(result.token ? { token: result.token } : {})
          });
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
