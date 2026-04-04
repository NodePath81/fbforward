import { describe, expect, it } from 'vitest';

import worker, { type Env } from '../src/worker';

class FakeStub {
  requests: Request[] = [];

  async fetch(request: Request): Promise<Response> {
    this.requests.push(request);
    return new Response('proxied', { status: 200 });
  }
}

class FakeNamespace implements DurableObjectNamespace {
  lastName = '';
  readonly stub = new FakeStub();

  idFromName(name: string): DurableObjectId {
    this.lastName = name;
    return {} as DurableObjectId;
  }

  get(): DurableObjectStub {
    return this.stub;
  }
}

describe('worker fetch', () => {
  it('serves healthz without auth', async () => {
    const env: Env = {
      FBCOORD_POOL: new FakeNamespace(),
      FBCOORD_TOKEN: 'secret-token'
    };

    const response = await worker.fetch(new Request('https://example.com/healthz'), env);

    expect(response.status).toBe(200);
    expect(await response.text()).toBe('ok');
  });

  it('rejects unauthorized node requests before durable object routing', async () => {
    const namespace = new FakeNamespace();
    const env: Env = {
      FBCOORD_POOL: namespace,
      FBCOORD_TOKEN: 'secret-token'
    };

    const response = await worker.fetch(new Request('https://example.com/ws/node?pool=default'), env);

    expect(response.status).toBe(401);
    expect(namespace.stub.requests).toHaveLength(0);
  });

  it('routes authorized node requests to the pool durable object', async () => {
    const namespace = new FakeNamespace();
    const env: Env = {
      FBCOORD_POOL: namespace,
      FBCOORD_TOKEN: 'secret-token'
    };

    const request = new Request('https://example.com/ws/node?pool=default', {
      headers: {
        Authorization: 'Bearer secret-token'
      }
    });

    const response = await worker.fetch(request, env);

    expect(response.status).toBe(200);
    expect(namespace.lastName).toBe('default');
    expect(namespace.stub.requests).toHaveLength(1);
  });
});
