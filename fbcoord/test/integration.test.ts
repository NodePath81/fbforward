import { describe, expect, it } from 'vitest';

import { activeBanKey, AuthGuardDurableObject } from '../src/durable-objects/auth-guard';
import { AUTHENTICATED_NODE_ID_HEADER } from '../src/durable-objects/pool';
import { TokenDurableObject } from '../src/durable-objects/token';
import { createWorker, type Env } from '../src/worker';
import { FactoryNamespace, jsonResponse, MemoryKV, MemoryStorage, RecordingStub, StaticNamespace } from './support';

const BOOTSTRAP_TOKEN = 'bootstrap-token-abcdefghijklmnopqrstuvwxyz123456';
const ROTATED_TOKEN = 'rotated-token-abcdefghijklmnopqrstuvwxyz789012';
const TOKEN_PEPPER = 'pepper-abcdefghijklmnopqrstuvwxyz1234567890';

interface StatePayload {
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

function cookieHeader(response: Response): string {
  return response.headers.get('Set-Cookie')?.split(';', 1)[0] ?? '';
}

function createEnv(state: StatePayload = {
  pick: { version: 0, upstream: null },
  node_count: 0,
  counts: {
    online: 0,
    offline: 0,
    aborted: 0
  },
  nodes: []
}): {
  env: Env;
  poolStub: RecordingStub;
  authKv: MemoryKV;
} {
  const poolStub = new RecordingStub(request => {
    const url = new URL(request.url);
    if (url.pathname === '/state') {
      return jsonResponse(state);
    }
    if (request.method === 'DELETE' && url.pathname.startsWith('/nodes/')) {
      const nodeId = decodeURIComponent(url.pathname.slice('/nodes/'.length));
      state.nodes = state.nodes.filter(node => node.node_id !== nodeId);
      state.node_count = state.nodes.filter(node => node.status === 'online').length;
      state.counts = {
        online: state.nodes.filter(node => node.status === 'online').length,
        offline: state.nodes.filter(node => node.status === 'offline').length,
        aborted: state.nodes.filter(node => node.status === 'aborted').length
      };
      return jsonResponse({ ok: true, removed: true });
    }
    return new Response('proxied', { status: 200 });
  });

  const poolNamespace = new StaticNamespace({
    global: poolStub
  });

  const tokenStore = new TokenDurableObject(
    { storage: new MemoryStorage() } as DurableObjectState,
    { FBCOORD_TOKEN: BOOTSTRAP_TOKEN, FBCOORD_TOKEN_PEPPER: TOKEN_PEPPER }
  );
  const authKv = new MemoryKV();

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
      FBCOORD_REGISTRY: new StaticNamespace({
        global: new RecordingStub()
      }),
      FBCOORD_TOKEN_STORE: tokenNamespace,
      FBCOORD_AUTH_GUARD: authGuardNamespace,
      FBCOORD_AUTH_KV: authKv,
      FBCOORD_TOKEN: BOOTSTRAP_TOKEN,
      FBCOORD_TOKEN_PEPPER: TOKEN_PEPPER
    },
    poolStub,
    authKv
  };
}

async function login(worker: ReturnType<typeof createWorker>, env: Env, token: string = BOOTSTRAP_TOKEN): Promise<Response> {
  return worker.fetch(new Request('https://example.com/api/auth/login', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ token })
  }), env);
}

async function createNodeToken(
  worker: ReturnType<typeof createWorker>,
  env: Env,
  cookie: string,
  nodeId: string
): Promise<{ token: string }> {
  const response = await worker.fetch(new Request('https://example.com/api/node-tokens', {
    method: 'POST',
    headers: {
      'content-type': 'application/json',
      Cookie: cookie
    },
    body: JSON.stringify({ node_id: nodeId })
  }), env);
  expect(response.status).toBe(200);
  return response.json() as Promise<{ token: string }>;
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

  it('rejects the operator token on the node websocket route after upgrade', async () => {
    const { env, poolStub } = createEnv();
    const worker = createWorker();

    const response = await worker.fetch(new Request('https://example.com/ws/node?pool=default', {
      headers: {
        Authorization: `Bearer ${BOOTSTRAP_TOKEN}`
      }
    }), env);

    expect(response.status).toBe(401);
    expect(poolStub.requests).toHaveLength(0);
  });

  it('authenticates node tokens on /ws/node, ignores the pool query, and injects node identity', async () => {
    const { env, poolStub } = createEnv();
    const worker = createWorker();

    const loginResponse = await login(worker, env);
    const cookie = cookieHeader(loginResponse);
    const created = await createNodeToken(worker, env, cookie, 'node-1');

    const response = await worker.fetch(new Request('https://example.com/ws/node?pool=default', {
      headers: {
        Authorization: `Bearer ${created.token}`
      }
    }), env);

    expect(response.status).toBe(200);
    expect(poolStub.requests).toHaveLength(1);
    expect(poolStub.requests[0]?.headers.get(AUTHENTICATED_NODE_ID_HEADER)).toBe('node-1');
  });

  it('rejects node tokens on the operator login route', async () => {
    const { env } = createEnv();
    const worker = createWorker();

    const loginResponse = await login(worker, env);
    const created = await createNodeToken(worker, env, cookieHeader(loginResponse), 'node-1');

    const response = await login(worker, env, created.token);

    expect(response.status).toBe(401);
  });

  it('creates a session on login and allows authenticated reads from /api/state', async () => {
    const { env } = createEnv({
      pick: { version: 3, upstream: 'us-a' },
      node_count: 1,
      counts: {
        online: 1,
        offline: 0,
        aborted: 0
      },
      nodes: [
        {
          node_id: 'node-1',
          status: 'online',
          first_seen_at: 500,
          last_connected_at: 500,
          last_seen_at: 1_000,
          disconnected_at: null,
          upstreams: ['us-a', 'us-b'],
          active_upstream: 'us-a'
        }
      ]
    });
    const worker = createWorker();

    const loginResponse = await login(worker, env);

    const stateResponse = await worker.fetch(new Request('https://example.com/api/state', {
      headers: {
        Cookie: cookieHeader(loginResponse)
      }
    }), env);

    expect(stateResponse.status).toBe(200);
    await expect(stateResponse.json()).resolves.toEqual({
      pick: { version: 3, upstream: 'us-a' },
      node_count: 1,
      counts: {
        online: 1,
        offline: 0,
        aborted: 0,
        never_seen: 0
      },
      nodes: [
        {
          node_id: 'node-1',
          status: 'online',
          first_seen_at: 500,
          last_connected_at: 500,
          last_seen_at: 1_000,
          disconnected_at: null,
          upstreams: ['us-a', 'us-b'],
          active_upstream: 'us-a'
        }
      ]
    });
  });

  it('synthesizes never_seen entries for provisioned node tokens missing from the live roster', async () => {
    const { env } = createEnv();
    const worker = createWorker();

    const loginResponse = await login(worker, env);
    const cookie = cookieHeader(loginResponse);
    await createNodeToken(worker, env, cookie, 'node-1');

    const response = await worker.fetch(new Request('https://example.com/api/state', {
      headers: {
        Cookie: cookie
      }
    }), env);

    expect(response.status).toBe(200);
    await expect(response.json()).resolves.toEqual({
      pick: { version: 0, upstream: null },
      node_count: 0,
      counts: {
        online: 0,
        offline: 0,
        aborted: 0,
        never_seen: 1
      },
      nodes: [
        {
          node_id: 'node-1',
          status: 'never_seen',
          first_seen_at: null,
          last_connected_at: null,
          last_seen_at: null,
          disconnected_at: null,
          upstreams: [],
          active_upstream: null
        }
      ]
    });
  });

  it('requires an operator session for node-token management APIs', async () => {
    const { env } = createEnv();
    const worker = createWorker();

    const listResponse = await worker.fetch(new Request('https://example.com/api/node-tokens'), env);
    expect(listResponse.status).toBe(401);

    const createResponse = await worker.fetch(new Request('https://example.com/api/node-tokens', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ node_id: 'node-1' })
    }), env);
    expect(createResponse.status).toBe(401);

    const revokeResponse = await worker.fetch(new Request('https://example.com/api/node-tokens/node-1', {
      method: 'DELETE'
    }), env);
    expect(revokeResponse.status).toBe(401);
  });

  it('lists, mints, and revokes node tokens through the authenticated API', async () => {
    const { env, poolStub } = createEnv();
    const worker = createWorker();

    const loginResponse = await login(worker, env);
    const cookie = cookieHeader(loginResponse);

    const created = await createNodeToken(worker, env, cookie, 'node-1');
    expect(created.token.length).toBeGreaterThanOrEqual(32);

    const listResponse = await worker.fetch(new Request('https://example.com/api/node-tokens', {
      headers: {
        Cookie: cookie
      }
    }), env);
    expect(listResponse.status).toBe(200);
    await expect(listResponse.json()).resolves.toEqual({
      tokens: [
        {
          node_id: 'node-1',
          masked_prefix: `${created.token.slice(0, 8)}...`,
          created_at: expect.any(Number),
          last_used_at: null
        }
      ]
    });

    const revokeResponse = await worker.fetch(new Request('https://example.com/api/node-tokens/node-1', {
      method: 'DELETE',
      headers: {
        Cookie: cookie
      }
    }), env);
    expect(revokeResponse.status).toBe(200);
    expect(poolStub.requests.some(request => new URL(request.url).pathname === '/nodes/node-1' && request.method === 'DELETE')).toBe(true);

    const nodeResponse = await worker.fetch(new Request('https://example.com/ws/node', {
      headers: {
        Authorization: `Bearer ${created.token}`
      }
    }), env);
    expect(nodeResponse.status).toBe(401);
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
    const loginResponse = await login(worker, env);
    const created = await createNodeToken(worker, env, cookieHeader(loginResponse), 'node-1');

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

    const nodeAuth = await worker.fetch(new Request('https://example.com/ws/node', {
      headers: {
        Authorization: `Bearer ${created.token}`,
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

  it('requires current_token for operator token rotation', async () => {
    const { env } = createEnv();
    const worker = createWorker();

    const loginResponse = await login(worker, env);

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

  it('rejects operator token rotation when current_token is wrong', async () => {
    const { env } = createEnv();
    const worker = createWorker();

    const loginResponse = await login(worker, env);

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

  it('keeps the current session valid after operator token rotation and leaves node tokens valid', async () => {
    const { env } = createEnv();
    const worker = createWorker();

    const loginResponse = await login(worker, env);
    const cookie = cookieHeader(loginResponse);
    const created = await createNodeToken(worker, env, cookie, 'node-1');

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

    expect((await login(worker, env, BOOTSTRAP_TOKEN)).status).toBe(401);
    expect((await login(worker, env, ROTATED_TOKEN)).status).toBe(200);

    const oldTokenResponse = await worker.fetch(new Request('https://example.com/ws/node', {
      headers: {
        Authorization: `Bearer ${BOOTSTRAP_TOKEN}`
      }
    }), env);
    expect(oldTokenResponse.status).toBe(401);

    const nodeTokenResponse = await worker.fetch(new Request('https://example.com/ws/node', {
      headers: {
        Authorization: `Bearer ${created.token}`
      }
    }), env);
    expect(nodeTokenResponse.status).toBe(200);
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

    const allowed = await login(worker, env);
    expect(allowed.status).toBe(200);
  });

  it('clears the session cookie on logout', async () => {
    const { env } = createEnv();
    const worker = createWorker();

    const loginResponse = await login(worker, env);

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
});
