import { describe, expect, it } from 'vitest';

import { RegistryDurableObject } from '../src/durable-objects/registry';
import { TokenDurableObject } from '../src/durable-objects/token';
import { createWorker, type Env } from '../src/worker';
import { jsonResponse, MemoryStorage, RecordingStub, StaticNamespace } from './support';

const BOOTSTRAP_TOKEN = 'bootstrap-token-abcdefghijklmnopqrstuvwxyz123456';
const ROTATED_TOKEN = 'rotated-token-abcdefghijklmnopqrstuvwxyz789012';

function cookieHeader(response: Response): string {
  return response.headers.get('Set-Cookie')?.split(';', 1)[0] ?? '';
}

function createEnv(poolState: Record<string, unknown> = {}): {
  env: Env;
  poolNamespace: StaticNamespace;
  poolStub: RecordingStub;
  registry: RegistryDurableObject;
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
    { FBCOORD_TOKEN: BOOTSTRAP_TOKEN }
  );

  const registryNamespace = new StaticNamespace({
    global: new RecordingStub(request => registry.fetch(request))
  });

  const tokenNamespace = new StaticNamespace({
    global: new RecordingStub(request => tokenStore.fetch(request))
  });

  return {
    env: {
      FBCOORD_POOL: poolNamespace,
      FBCOORD_REGISTRY: registryNamespace,
      FBCOORD_TOKEN_STORE: tokenNamespace,
      FBCOORD_TOKEN: BOOTSTRAP_TOKEN
    },
    poolNamespace,
    poolStub,
    registry
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

  it('rate-limits repeated failed logins', async () => {
    const { env } = createEnv();
    const worker = createWorker();
    const makeRequest = () => new Request('https://example.com/api/auth/login', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        'cf-connecting-ip': '203.0.113.8'
      },
      body: JSON.stringify({ token: 'wrong-token-value-abcdefghijklmnopqrstuvwxyz' })
    });

    expect((await worker.fetch(makeRequest(), env)).status).toBe(401);
    expect((await worker.fetch(makeRequest(), env)).status).toBe(401);
    expect((await worker.fetch(makeRequest(), env)).status).toBe(401);
    expect((await worker.fetch(makeRequest(), env)).status).toBe(429);
  });

  it('keeps the current session valid after token rotation', async () => {
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
      body: JSON.stringify({ token: ROTATED_TOKEN })
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
});
