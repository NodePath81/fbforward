import { describe, expect, it } from 'vitest';

import { activeBanKey, AuthGuardDurableObject } from '../src/durable-objects/auth-guard';
import { RegistryDurableObject } from '../src/durable-objects/registry';
import { TokenDurableObject } from '../src/durable-objects/token';
import { createWorker, type Env } from '../src/worker';
import { FactoryNamespace, jsonResponse, MemoryKV, MemoryStorage, RecordingStub, StaticNamespace } from './support';

const BOOTSTRAP_TOKEN = 'bootstrap-token-abcdefghijklmnopqrstuvwxyz123456';
const ROTATED_TOKEN = 'rotated-token-abcdefghijklmnopqrstuvwxyz789012';
const TOKEN_PEPPER = 'pepper-abcdefghijklmnopqrstuvwxyz1234567890';

function cookieHeader(response: Response): string {
  return response.headers.get('Set-Cookie')?.split(';', 1)[0] ?? '';
}

function createEnv(poolState: Record<string, unknown> = {}): {
  env: Env;
  poolNamespace: StaticNamespace;
  poolStub: RecordingStub;
  registry: RegistryDurableObject;
  authKv: MemoryKV;
} {
  const poolStub = new RecordingStub(request => {
    const url = new URL(request.url);
    if (url.pathname === '/state') {
      const pool = url.searchParams.get('pool') ?? 'default';
      return jsonResponse(poolState[pool] ?? {
        pool,
        pick: { version: 0, upstream: null },
        node_count: 0,
        nodes: []
      });
    }
    return new Response('proxied', { status: 200 });
  });

  const poolNamespace = new StaticNamespace({
    default: poolStub,
    alpha: poolStub
  });

  const registry = new RegistryDurableObject({ storage: new MemoryStorage() } as DurableObjectState);
  const tokenStore = new TokenDurableObject(
    { storage: new MemoryStorage() } as DurableObjectState,
    { FBCOORD_TOKEN: BOOTSTRAP_TOKEN, FBCOORD_TOKEN_PEPPER: TOKEN_PEPPER }
  );
  const authKv = new MemoryKV();

  const registryNamespace = new StaticNamespace({
    global: new RecordingStub(request => registry.fetch(request))
  });

  const tokenNamespace = new StaticNamespace({
    global: new RecordingStub(request => tokenStore.fetch(request))
  });

  const authGuardNamespace = new FactoryNamespace(name => {
    const guard = new AuthGuardDurableObject(
      { storage: new MemoryStorage() } as DurableObjectState,
      { FBCOORD_AUTH_KV: authKv }
    );
    return new RecordingStub(request => guard.fetch(request));
  });

  return {
    env: {
      FBCOORD_POOL: poolNamespace,
      FBCOORD_REGISTRY: registryNamespace,
      FBCOORD_TOKEN_STORE: tokenNamespace,
      FBCOORD_AUTH_GUARD: authGuardNamespace,
      FBCOORD_AUTH_KV: authKv,
      FBCOORD_TOKEN: BOOTSTRAP_TOKEN,
      FBCOORD_TOKEN_PEPPER: TOKEN_PEPPER
    },
    poolNamespace,
    poolStub,
    registry,
    authKv
  };
}

describe('worker fetch', () => {
  it('serves healthz without auth', async () => {
    const { env } = createEnv();
    const worker = createWorker();

    const response = await worker.fetch(new Request('https://example.com/healthz'), env);

    expect(response.status).toBe(200);
    expect(await response.text()).toBe('ok');
  });

  it('rejects unauthorized node requests before durable object routing', async () => {
    const { env, poolStub } = createEnv();
    const worker = createWorker();

    const response = await worker.fetch(new Request('https://example.com/ws/node?pool=default'), env);

    expect(response.status).toBe(401);
    expect(poolStub.requests).toHaveLength(0);
  });

  it('routes authorized node requests to the pool durable object', async () => {
    const { env, poolStub } = createEnv();
    const worker = createWorker();

    const request = new Request('https://example.com/ws/node?pool=default', {
      headers: {
        Authorization: `Bearer ${BOOTSTRAP_TOKEN}`
      }
    });

    const response = await worker.fetch(request, env);

    expect(response.status).toBe(200);
    expect(poolStub.requests).toHaveLength(1);
  });

  it('creates a session on login and allows authenticated API reads', async () => {
    const poolState = {
      alpha: {
        pool: 'alpha',
        pick: { version: 3, upstream: 'us-a' },
        node_count: 1,
        nodes: [
          {
            node_id: 'node-1',
            upstreams: ['us-a', 'us-b'],
            active_upstream: 'us-a',
            last_seen: 1_000,
            connected_at: 500
          }
        ]
      }
    };
    const { env, registry } = createEnv(poolState);
    const worker = createWorker();

    await registry.fetch(new Request('https://registry.internal/register', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ pool: 'alpha' })
    }));

    const loginResponse = await worker.fetch(new Request('https://example.com/api/auth/login', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ token: BOOTSTRAP_TOKEN })
    }), env);

    expect(loginResponse.status).toBe(200);

    const poolsResponse = await worker.fetch(new Request('https://example.com/api/pools', {
      headers: {
        Cookie: cookieHeader(loginResponse)
      }
    }), env);

    expect(poolsResponse.status).toBe(200);
    await expect(poolsResponse.json()).resolves.toEqual({
      pools: [
        {
          name: 'alpha',
          node_count: 1,
          pick: { version: 3, upstream: 'us-a' }
        }
      ]
    });
  });

  it('rate-limits repeated failed logins across worker instances', async () => {
    const { env } = createEnv();
    const workerA = createWorker();
    const workerB = createWorker();
    const makeRequest = () => new Request('https://example.com/api/auth/login', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        'cf-connecting-ip': '203.0.113.8'
      },
      body: JSON.stringify({ token: 'wrong-token-value-abcdefghijklmnopqrstuvwxyz' })
    });

    expect((await workerA.fetch(makeRequest(), env)).status).toBe(401);
    expect((await workerB.fetch(makeRequest(), env)).status).toBe(401);
    expect((await workerA.fetch(makeRequest(), env)).status).toBe(401);
    expect((await workerB.fetch(makeRequest(), env)).status).toBe(429);
  });

  it('does not let node auth reset failed login attempts from the same client key', async () => {
    const { env } = createEnv();
    const worker = createWorker();

    const badLogin = () => new Request('https://example.com/api/auth/login', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        'cf-connecting-ip': '203.0.113.9'
      },
      body: JSON.stringify({ token: 'wrong-token-value-abcdefghijklmnopqrstuvwxyz' })
    });

    expect((await worker.fetch(badLogin(), env)).status).toBe(401);
    expect((await worker.fetch(badLogin(), env)).status).toBe(401);

    const nodeAuth = await worker.fetch(new Request('https://example.com/ws/node?pool=default', {
      headers: {
        Authorization: `Bearer ${BOOTSTRAP_TOKEN}`,
        'cf-connecting-ip': '203.0.113.9'
      }
    }), env);
    expect(nodeAuth.status).toBe(200);

    expect((await worker.fetch(badLogin(), env)).status).toBe(401);
    expect((await worker.fetch(badLogin(), env)).status).toBe(429);
  });

  it('blocks immediately when the KV ban cache contains an active ban', async () => {
    const { env, authKv } = createEnv();
    const worker = createWorker();
    await authKv.put(activeBanKey('login', '203.0.113.10'), JSON.stringify({
      blocked_until: Date.now() + 60_000
    }));

    const response = await worker.fetch(new Request('https://example.com/api/auth/login', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        'cf-connecting-ip': '203.0.113.10'
      },
      body: JSON.stringify({ token: BOOTSTRAP_TOKEN })
    }), env);

    expect(response.status).toBe(429);
  });

  it('requires current_token for token rotation', async () => {
    const { env } = createEnv();
    const worker = createWorker();

    const loginResponse = await worker.fetch(new Request('https://example.com/api/auth/login', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ token: BOOTSTRAP_TOKEN })
    }), env);

    const response = await worker.fetch(new Request('https://example.com/api/token/rotate', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        Cookie: cookieHeader(loginResponse)
      },
      body: JSON.stringify({ token: ROTATED_TOKEN })
    }), env);

    expect(response.status).toBe(401);
  });

  it('rejects token rotation when current_token is wrong', async () => {
    const { env } = createEnv();
    const worker = createWorker();

    const loginResponse = await worker.fetch(new Request('https://example.com/api/auth/login', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ token: BOOTSTRAP_TOKEN })
    }), env);

    const response = await worker.fetch(new Request('https://example.com/api/token/rotate', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        Cookie: cookieHeader(loginResponse)
      },
      body: JSON.stringify({
        current_token: 'wrong-token-value-abcdefghijklmnopqrstuvwxyz',
        token: ROTATED_TOKEN
      })
    }), env);

    expect(response.status).toBe(401);
  });

  it('keeps the current session valid after token rotation when current_token is supplied', async () => {
    const { env } = createEnv();
    const worker = createWorker();

    const loginResponse = await worker.fetch(new Request('https://example.com/api/auth/login', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ token: BOOTSTRAP_TOKEN })
    }), env);

    const cookie = cookieHeader(loginResponse);

    const rotateResponse = await worker.fetch(new Request('https://example.com/api/token/rotate', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        Cookie: cookie
      },
      body: JSON.stringify({ current_token: BOOTSTRAP_TOKEN, token: ROTATED_TOKEN })
    }), env);

    expect(rotateResponse.status).toBe(200);

    const authCheck = await worker.fetch(new Request('https://example.com/api/auth/check', {
      headers: {
        Cookie: cookie
      }
    }), env);
    expect(authCheck.status).toBe(200);

    const oldTokenResponse = await worker.fetch(new Request('https://example.com/ws/node?pool=default', {
      headers: {
        Authorization: `Bearer ${BOOTSTRAP_TOKEN}`
      }
    }), env);
    expect(oldTokenResponse.status).toBe(401);

    const newTokenResponse = await worker.fetch(new Request('https://example.com/ws/node?pool=default', {
      headers: {
        Authorization: `Bearer ${ROTATED_TOKEN}`
      }
    }), env);
    expect(newTokenResponse.status).toBe(200);
  });

  it('rejects invalid origins on mutating endpoints and allows missing Origin', async () => {
    const { env } = createEnv();
    const worker = createWorker();

    const forbidden = await worker.fetch(new Request('https://example.com/api/auth/login', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        Origin: 'https://evil.example'
      },
      body: JSON.stringify({ token: BOOTSTRAP_TOKEN })
    }), env);
    expect(forbidden.status).toBe(403);

    const allowed = await worker.fetch(new Request('https://example.com/api/auth/login', {
      method: 'POST',
      headers: {
        'content-type': 'application/json'
      },
      body: JSON.stringify({ token: BOOTSTRAP_TOKEN })
    }), env);
    expect(allowed.status).toBe(200);
  });

  it('clears the session cookie on logout', async () => {
    const { env } = createEnv();
    const worker = createWorker();

    const loginResponse = await worker.fetch(new Request('https://example.com/api/auth/login', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ token: BOOTSTRAP_TOKEN })
    }), env);

    const logoutResponse = await worker.fetch(new Request('https://example.com/api/auth/logout', {
      method: 'POST',
      headers: {
        Cookie: cookieHeader(loginResponse)
      }
    }), env);

    expect(logoutResponse.status).toBe(200);

    const authCheck = await worker.fetch(new Request('https://example.com/api/auth/check', {
      headers: {
        Cookie: cookieHeader(logoutResponse)
      }
    }), env);
    expect(authCheck.status).toBe(401);
  });

  it('rejects invalid pool names before routing to the pool durable object', async () => {
    const { env, poolStub } = createEnv();
    const worker = createWorker();

    const response = await worker.fetch(new Request('https://example.com/ws/node?pool=../bad', {
      headers: {
        Authorization: `Bearer ${BOOTSTRAP_TOKEN}`
      }
    }), env);

    expect(response.status).toBe(400);
    expect(poolStub.requests).toHaveLength(0);
  });
});
