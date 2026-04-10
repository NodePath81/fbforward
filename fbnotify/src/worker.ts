import { getCookie, isAllowedOrigin } from './auth';
import { validateNotificationEvent } from './schema';
import { clearSessionCookie, createSession, createSessionCookie, SESSION_COOKIE_NAME, validateSession } from './session';
import type {
  CreateNodeTokenResponse,
  DeliveryResult,
  NodeTokenInfo,
  NotificationEvent,
  OperatorTokenInfo,
  ProviderTargetRecord,
  ProviderTargetSummary,
  RouteSummary
} from './types';
import { deliverToTarget } from './providers';
import { CaptureDurableObject } from './durable-objects/capture';
import { ConfigDurableObject } from './durable-objects/config';
import { TokenDurableObject } from './durable-objects/token';

const GLOBAL_OBJECT_NAME = 'global';

export interface Env {
  FBNOTIFY_CONFIG: DurableObjectNamespace;
  FBNOTIFY_TOKEN_STORE: DurableObjectNamespace;
  FBNOTIFY_CAPTURE: DurableObjectNamespace;
  FBNOTIFY_OPERATOR_TOKEN: string;
  FBNOTIFY_TOKEN_PEPPER: string;
  ASSETS?: Fetcher;
}

interface ExecutionContextLike {
  waitUntil(promise: Promise<unknown>): void;
}

interface ListNodeTokensResponse {
  tokens: NodeTokenInfo[];
}

interface ListTargetsResponse {
  targets: ProviderTargetSummary[];
}

interface ListRoutesResponse {
  routes: RouteSummary[];
}

interface ResolveTargetsResponse {
  targets: ProviderTargetRecord[];
}

interface SessionSecretResponse {
  session_secret: string;
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

function configStub(env: Env): DurableObjectStub {
  return env.FBNOTIFY_CONFIG.get(env.FBNOTIFY_CONFIG.idFromName(GLOBAL_OBJECT_NAME));
}

function tokenStub(env: Env): DurableObjectStub {
  return env.FBNOTIFY_TOKEN_STORE.get(env.FBNOTIFY_TOKEN_STORE.idFromName(GLOBAL_OBJECT_NAME));
}

function captureStub(env: Env): DurableObjectStub {
  return env.FBNOTIFY_CAPTURE.get(env.FBNOTIFY_CAPTURE.idFromName(GLOBAL_OBJECT_NAME));
}

async function validateOperatorToken(env: Env, token: string): Promise<boolean> {
  const response = await tokenStub(env).fetch(new Request('https://token.internal/validate-operator', {
    method: 'POST',
    headers: {
      'content-type': 'application/json'
    },
    body: JSON.stringify({ token })
  }));
  if (!response.ok) {
    return false;
  }
  const body = await response.json() as { valid: boolean };
  return body.valid;
}

async function getSessionSecret(env: Env): Promise<string> {
  const response = await tokenStub(env).fetch('https://token.internal/session-secret');
  const body = await response.json() as SessionSecretResponse;
  return body.session_secret;
}

async function requireAuthenticatedSession(request: Request, env: Env): Promise<Response | null> {
  const cookie = getCookie(request, SESSION_COOKIE_NAME);
  if (!cookie) {
    return json({ error: 'unauthorized' }, 401);
  }
  const sessionSecret = await getSessionSecret(env);
  if (!(await validateSession(cookie, sessionSecret))) {
    return json({ error: 'unauthorized' }, 401);
  }
  return null;
}

async function listNodeTokens(env: Env): Promise<NodeTokenInfo[]> {
  const response = await tokenStub(env).fetch('https://token.internal/node-tokens');
  const body = await response.json() as ListNodeTokensResponse;
  return body.tokens;
}

async function listTargets(env: Env): Promise<ProviderTargetSummary[]> {
  const response = await configStub(env).fetch('https://config.internal/targets');
  const body = await response.json() as ListTargetsResponse;
  return body.targets;
}

async function listRoutes(env: Env): Promise<RouteSummary[]> {
  const response = await configStub(env).fetch('https://config.internal/routes');
  const body = await response.json() as ListRoutesResponse;
  return body.routes;
}

async function resolveTargets(env: Env, event: NotificationEvent): Promise<ProviderTargetRecord[]> {
  const response = await configStub(env).fetch(new Request('https://config.internal/resolve', {
    method: 'POST',
    headers: {
      'content-type': 'application/json'
    },
    body: JSON.stringify({ event })
  }));
  const body = await response.json() as ResolveTargetsResponse & { error?: string };
  if (!response.ok) {
    throw new Error(body.error ?? 'unable to resolve targets');
  }
  return body.targets;
}

async function targetsByIds(env: Env, targetIds: string[]): Promise<ProviderTargetRecord[]> {
  const response = await configStub(env).fetch(new Request('https://config.internal/targets/by-ids', {
    method: 'POST',
    headers: {
      'content-type': 'application/json'
    },
    body: JSON.stringify({ target_ids: targetIds })
  }));
  const body = await response.json() as ResolveTargetsResponse & { error?: string };
  if (!response.ok) {
    throw new Error(body.error ?? 'unable to resolve targets');
  }
  return body.targets;
}

async function deliverAll(env: Env, event: NotificationEvent, targets: ProviderTargetRecord[]): Promise<DeliveryResult[]> {
  return Promise.all(targets.map(target => deliverToTarget(target, event, captureStub(env), fetch)));
}

function noOpContext(): ExecutionContextLike {
  return {
    waitUntil(promise: Promise<unknown>): void {
      void promise;
    }
  };
}

function requireSameOrigin(request: Request): Response | null {
  if (!isAllowedOrigin(request)) {
    return json({ error: 'forbidden' }, 403);
  }
  return null;
}

function createWorker() {
  return {
    async fetch(request: Request, env: Env, ctx: ExecutionContextLike = noOpContext()): Promise<Response> {
      const url = new URL(request.url);

      if (url.pathname === '/healthz') {
        return new Response('ok', { status: 200 });
      }

      if (url.pathname === '/v1/events') {
        if (request.method !== 'POST') {
          return methodNotAllowed('POST');
        }
        const rawBody = await request.text();
        let parsed: unknown;
        try {
          parsed = JSON.parse(rawBody);
        } catch {
          return json({ error: 'invalid json' }, 400);
        }
        const validated = validateNotificationEvent(parsed);
        if (!validated.event) {
          return json({ error: validated.error ?? 'invalid event' }, 400);
        }

        const response = await tokenStub(env).fetch(new Request('https://token.internal/validate-ingress', {
          method: 'POST',
          headers: {
            'content-type': 'application/json'
          },
          body: JSON.stringify({
            key_id: request.headers.get('x-fbnotify-key-id'),
            timestamp: request.headers.get('x-fbnotify-timestamp'),
            signature: request.headers.get('x-fbnotify-signature'),
            raw_body: rawBody,
            source_service: validated.event.source.service,
            source_instance: validated.event.source.instance
          })
        }));
        const authBody = await parseJsonResponse<{ valid: boolean; error?: string }>(response);
        if (!response.ok || !authBody?.valid) {
          return json({ error: authBody?.error ?? 'unauthorized' }, 401);
        }

        const targets = await resolveTargets(env, validated.event);
        ctx.waitUntil(deliverAll(env, validated.event, targets));
        return json({ accepted: true, target_count: targets.length }, 202);
      }

      if (url.pathname === '/api/auth/login') {
        if (request.method !== 'POST') {
          return methodNotAllowed('POST');
        }
        const originError = requireSameOrigin(request);
        if (originError) {
          return originError;
        }
        const body = await parseJsonBody<{ token?: string }>(request);
        const token = body?.token?.trim();
        if (!token || !(await validateOperatorToken(env, token))) {
          return json({ error: 'invalid token' }, 401);
        }
        const sessionSecret = await getSessionSecret(env);
        const session = await createSession(sessionSecret);
        return json({ ok: true }, 200, {
          'Set-Cookie': createSessionCookie(session, undefined, secureCookiesFor(request))
        });
      }

      if (url.pathname.startsWith('/api/')) {
        const authError = await requireAuthenticatedSession(request, env);
        if (authError) {
          return authError;
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

        if (url.pathname === '/api/token/info') {
          if (request.method !== 'GET') {
            return methodNotAllowed('GET');
          }
          const response = await tokenStub(env).fetch('https://token.internal/info');
          return json(await response.json() as OperatorTokenInfo);
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
          const response = await tokenStub(env).fetch(new Request('https://token.internal/rotate', {
            method: 'POST',
            headers: {
              'content-type': 'application/json'
            },
            body: JSON.stringify({
              token: body.token,
              generate: body.generate
            })
          }));
          const result = await parseJsonResponse<OperatorTokenInfo & { token?: string; error?: string }>(response);
          if (!response.ok || !result) {
            return json({ error: result?.error ?? 'invalid token' }, response.status);
          }
          return json(result);
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
            const body = await parseJsonBody<{ source_service?: string; source_instance?: string }>(request);
            if (!body) {
              return json({ error: 'invalid json' }, 400);
            }
            const response = await tokenStub(env).fetch(new Request('https://token.internal/node-tokens', {
              method: 'POST',
              headers: {
                'content-type': 'application/json'
              },
              body: JSON.stringify(body)
            }));
            const result = await parseJsonResponse<CreateNodeTokenResponse & { error?: string }>(response);
            if (!response.ok || !result) {
              return json({ error: result?.error ?? 'invalid node token request' }, response.status);
            }
            return json(result);
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
          const response = await tokenStub(env).fetch(new Request(`https://token.internal/node-tokens/${url.pathname.slice('/api/node-tokens/'.length)}`, {
            method: 'DELETE'
          }));
          const result = await parseJsonResponse<{ ok?: boolean; error?: string }>(response);
          if (!response.ok || !result?.ok) {
            return json({ error: result?.error ?? 'unable to revoke node token' }, response.status);
          }
          return json({ ok: true });
        }

        if (url.pathname === '/api/targets') {
          if (request.method === 'GET') {
            return json({ targets: await listTargets(env) });
          }
          if (request.method === 'POST') {
            const originError = requireSameOrigin(request);
            if (originError) {
              return originError;
            }
            const body = await parseJsonBody<Record<string, unknown>>(request);
            if (!body) {
              return json({ error: 'invalid json' }, 400);
            }
            const response = await configStub(env).fetch(new Request('https://config.internal/targets', {
              method: 'POST',
              headers: {
                'content-type': 'application/json'
              },
              body: JSON.stringify(body)
            }));
            const result = await parseJsonResponse<ProviderTargetSummary & { error?: string }>(response);
            if (!response.ok || !result) {
              return json({ error: result?.error ?? 'invalid target request' }, response.status);
            }
            return json(result);
          }
          return methodNotAllowed('GET, POST');
        }

        if (url.pathname.startsWith('/api/targets/')) {
          const targetId = url.pathname.slice('/api/targets/'.length);
          if (request.method === 'PUT') {
            const originError = requireSameOrigin(request);
            if (originError) {
              return originError;
            }
            const body = await parseJsonBody<Record<string, unknown>>(request);
            if (!body) {
              return json({ error: 'invalid json' }, 400);
            }
            const response = await configStub(env).fetch(new Request(`https://config.internal/targets/${targetId}`, {
              method: 'PUT',
              headers: {
                'content-type': 'application/json'
              },
              body: JSON.stringify(body)
            }));
            const result = await parseJsonResponse<ProviderTargetSummary & { error?: string }>(response);
            if (!response.ok || !result) {
              return json({ error: result?.error ?? 'invalid target request' }, response.status);
            }
            return json(result);
          }
          if (request.method === 'DELETE') {
            const originError = requireSameOrigin(request);
            if (originError) {
              return originError;
            }
            const response = await configStub(env).fetch(new Request(`https://config.internal/targets/${targetId}`, {
              method: 'DELETE'
            }));
            const result = await parseJsonResponse<{ ok?: boolean; error?: string }>(response);
            if (!response.ok || !result?.ok) {
              return json({ error: result?.error ?? 'unable to delete target' }, response.status);
            }
            return json({ ok: true });
          }
          return methodNotAllowed('PUT, DELETE');
        }

        if (url.pathname === '/api/routes') {
          if (request.method === 'GET') {
            return json({ routes: await listRoutes(env) });
          }
          if (request.method === 'POST') {
            const originError = requireSameOrigin(request);
            if (originError) {
              return originError;
            }
            const body = await parseJsonBody<Record<string, unknown>>(request);
            if (!body) {
              return json({ error: 'invalid json' }, 400);
            }
            const response = await configStub(env).fetch(new Request('https://config.internal/routes', {
              method: 'POST',
              headers: {
                'content-type': 'application/json'
              },
              body: JSON.stringify(body)
            }));
            const result = await parseJsonResponse<RouteSummary & { error?: string }>(response);
            if (!response.ok || !result) {
              return json({ error: result?.error ?? 'invalid route request' }, response.status);
            }
            return json(result);
          }
          return methodNotAllowed('GET, POST');
        }

        if (url.pathname.startsWith('/api/routes/')) {
          const routeId = url.pathname.slice('/api/routes/'.length);
          if (request.method === 'PUT') {
            const originError = requireSameOrigin(request);
            if (originError) {
              return originError;
            }
            const body = await parseJsonBody<Record<string, unknown>>(request);
            if (!body) {
              return json({ error: 'invalid json' }, 400);
            }
            const response = await configStub(env).fetch(new Request(`https://config.internal/routes/${routeId}`, {
              method: 'PUT',
              headers: {
                'content-type': 'application/json'
              },
              body: JSON.stringify(body)
            }));
            const result = await parseJsonResponse<RouteSummary & { error?: string }>(response);
            if (!response.ok || !result) {
              return json({ error: result?.error ?? 'invalid route request' }, response.status);
            }
            return json(result);
          }
          if (request.method === 'DELETE') {
            const originError = requireSameOrigin(request);
            if (originError) {
              return originError;
            }
            const response = await configStub(env).fetch(new Request(`https://config.internal/routes/${routeId}`, {
              method: 'DELETE'
            }));
            const result = await parseJsonResponse<{ ok?: boolean; error?: string }>(response);
            if (!response.ok || !result?.ok) {
              return json({ error: result?.error ?? 'unable to delete route' }, response.status);
            }
            return json({ ok: true });
          }
          return methodNotAllowed('PUT, DELETE');
        }

        if (url.pathname === '/api/test-send') {
          if (request.method !== 'POST') {
            return methodNotAllowed('POST');
          }
          const originError = requireSameOrigin(request);
          if (originError) {
            return originError;
          }
          const body = await parseJsonBody<{ event?: unknown; target_ids?: string[] }>(request);
          if (!body) {
            return json({ error: 'invalid json' }, 400);
          }
          const validated = validateNotificationEvent(body.event);
          if (!validated.event) {
            return json({ error: validated.error ?? 'invalid event' }, 400);
          }
          const targets = body.target_ids && body.target_ids.length > 0
            ? await targetsByIds(env, body.target_ids)
            : await resolveTargets(env, validated.event);
          const results = await deliverAll(env, validated.event, targets);
          return json({
            target_count: targets.length,
            results
          });
        }

        if (url.pathname === '/api/capture/messages') {
          if (request.method !== 'GET') {
            return methodNotAllowed('GET');
          }
          const response = await captureStub(env).fetch('https://capture.internal/messages');
          return json(await response.json());
        }

        if (url.pathname === '/api/capture/clear') {
          if (request.method !== 'POST') {
            return methodNotAllowed('POST');
          }
          const originError = requireSameOrigin(request);
          if (originError) {
            return originError;
          }
          const response = await captureStub(env).fetch(new Request('https://capture.internal/clear', {
            method: 'POST'
          }));
          return json(await response.json());
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
export { CaptureDurableObject, ConfigDurableObject, createWorker, TokenDurableObject };
