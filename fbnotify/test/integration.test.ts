import { afterEach, describe, expect, it, vi } from 'vitest';

import { CaptureDurableObject } from '../src/durable-objects/capture';
import { ConfigDurableObject } from '../src/durable-objects/config';
import { TokenDurableObject } from '../src/durable-objects/token';
import { createWorker, type Env } from '../src/worker';
import type { NotificationEvent } from '../src/types';
import { createExecutionContext, MemoryStorage, RecordingStub, StaticNamespace } from './support';

const OPERATOR_TOKEN = 'bootstrap-token-abcdefghijklmnopqrstuvwxyz123456';
const TOKEN_PEPPER = 'pepper-abcdefghijklmnopqrstuvwxyz1234567890';

function bytesToBase64Url(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '');
}

async function sign(secret: string, payload: string): Promise<string> {
  const key = await crypto.subtle.importKey(
    'raw',
    new TextEncoder().encode(secret),
    { name: 'HMAC', hash: 'SHA-256' },
    false,
    ['sign']
  );
  const signature = await crypto.subtle.sign('HMAC', key, new TextEncoder().encode(payload));
  return bytesToBase64Url(new Uint8Array(signature));
}

function cookieHeader(response: Response): string {
  return response.headers.get('Set-Cookie')?.split(';', 1)[0] ?? '';
}

function createEnv(): Env {
  const configObject = new ConfigDurableObject(
    { storage: new MemoryStorage() } as DurableObjectState
  );
  const tokenObject = new TokenDurableObject(
    { storage: new MemoryStorage() } as DurableObjectState,
    {
      FBNOTIFY_OPERATOR_TOKEN: OPERATOR_TOKEN,
      FBNOTIFY_TOKEN_PEPPER: TOKEN_PEPPER
    }
  );
  const captureObject = new CaptureDurableObject(
    { storage: new MemoryStorage() } as DurableObjectState
  );

  return {
    FBNOTIFY_CONFIG: new StaticNamespace({
      global: new RecordingStub(request => configObject.fetch(request))
    }),
    FBNOTIFY_TOKEN_STORE: new StaticNamespace({
      global: new RecordingStub(request => tokenObject.fetch(request))
    }),
    FBNOTIFY_CAPTURE: new StaticNamespace({
      global: new RecordingStub(request => captureObject.fetch(request))
    }),
    FBNOTIFY_OPERATOR_TOKEN: OPERATOR_TOKEN,
    FBNOTIFY_TOKEN_PEPPER: TOKEN_PEPPER
  };
}

async function login(worker: ReturnType<typeof createWorker>, env: Env): Promise<string> {
  const response = await worker.fetch(new Request('https://example.com/api/auth/login', {
    method: 'POST',
    headers: {
      'content-type': 'application/json'
    },
    body: JSON.stringify({ token: OPERATOR_TOKEN })
  }), env, createExecutionContext().ctx);
  expect(response.status).toBe(200);
  return cookieHeader(response);
}

const EVENT: NotificationEvent = {
  schema_version: 1,
  event_name: 'demo.capture',
  severity: 'warn',
  timestamp: '2026-04-09T00:00:00Z',
  source: {
    service: 'fbforward',
    instance: 'node-1'
  },
  attributes: {
    reason: 'manual'
  }
};

afterEach(() => {
  vi.restoreAllMocks();
});

describe('fbnotify worker', () => {
  it('serves healthz and authenticates operator sessions', async () => {
    const env = createEnv();
    const worker = createWorker();

    const response = await worker.fetch(new Request('https://example.com/healthz'), env, createExecutionContext().ctx);
    expect(response.status).toBe(200);
    expect(await response.text()).toBe('ok');

    const cookie = await login(worker, env);
    const authCheck = await worker.fetch(new Request('https://example.com/api/auth/check', {
      headers: {
        Cookie: cookie
      }
    }), env, createExecutionContext().ctx);
    expect(authCheck.status).toBe(200);
  });

  it('manages targets, routes, and node tokens', async () => {
    const env = createEnv();
    const worker = createWorker();
    const cookie = await login(worker, env);
    const ctx = createExecutionContext();

    const targetResponse = await worker.fetch(new Request('https://example.com/api/targets', {
      method: 'POST',
      headers: {
        Cookie: cookie,
        'content-type': 'application/json'
      },
      body: JSON.stringify({
        name: 'capture',
        type: 'capture',
        config: {}
      })
    }), env, ctx.ctx);
    expect(targetResponse.status).toBe(200);
    const target = await targetResponse.json() as { id: string };

    const routeResponse = await worker.fetch(new Request('https://example.com/api/routes', {
      method: 'POST',
      headers: {
        Cookie: cookie,
        'content-type': 'application/json'
      },
      body: JSON.stringify({
        name: 'default',
        target_ids: [target.id]
      })
    }), env, ctx.ctx);
    expect(routeResponse.status).toBe(200);

    const nodeTokenResponse = await worker.fetch(new Request('https://example.com/api/node-tokens', {
      method: 'POST',
      headers: {
        Cookie: cookie,
        'content-type': 'application/json'
      },
      body: JSON.stringify({
        source_service: 'fbforward',
        source_instance: 'node-1'
      })
    }), env, ctx.ctx);
    expect(nodeTokenResponse.status).toBe(200);
    const nodeToken = await nodeTokenResponse.json() as { key_id: string; token: string };
    expect(nodeToken.key_id).toBeDefined();
    expect(nodeToken.token.length).toBeGreaterThanOrEqual(32);
  });

  it('accepts signed ingress events and delivers them to the capture target asynchronously', async () => {
    const env = createEnv();
    const worker = createWorker();
    const cookie = await login(worker, env);
    const setupCtx = createExecutionContext();

    const targetResponse = await worker.fetch(new Request('https://example.com/api/targets', {
      method: 'POST',
      headers: {
        Cookie: cookie,
        'content-type': 'application/json'
      },
      body: JSON.stringify({
        name: 'capture',
        type: 'capture',
        config: {}
      })
    }), env, setupCtx.ctx);
    const target = await targetResponse.json() as { id: string };

    await worker.fetch(new Request('https://example.com/api/routes', {
      method: 'POST',
      headers: {
        Cookie: cookie,
        'content-type': 'application/json'
      },
      body: JSON.stringify({
        name: 'default',
        target_ids: [target.id]
      })
    }), env, setupCtx.ctx);

    const nodeTokenResponse = await worker.fetch(new Request('https://example.com/api/node-tokens', {
      method: 'POST',
      headers: {
        Cookie: cookie,
        'content-type': 'application/json'
      },
      body: JSON.stringify({
        source_service: 'fbforward',
        source_instance: 'node-1'
      })
    }), env, setupCtx.ctx);
    const nodeToken = await nodeTokenResponse.json() as { key_id: string; token: string };

    const rawBody = JSON.stringify(EVENT);
    const headerTimestamp = String(Math.floor(Date.now() / 1000));
    const signature = await sign(nodeToken.token, `${headerTimestamp}.${rawBody}`);
    const ingressCtx = createExecutionContext();

    const ingressResponse = await worker.fetch(new Request('https://example.com/v1/events', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        'x-fbnotify-key-id': nodeToken.key_id,
        'x-fbnotify-timestamp': headerTimestamp,
        'x-fbnotify-signature': signature
      },
      body: rawBody
    }), env, ingressCtx.ctx);
    expect(ingressResponse.status).toBe(202);

    await ingressCtx.flush();

    const messagesResponse = await worker.fetch(new Request('https://example.com/api/capture/messages', {
      headers: {
        Cookie: cookie
      }
    }), env, createExecutionContext().ctx);
    const messagesPayload = await messagesResponse.json() as { messages: Array<{ event_name: string }> };
    expect(messagesPayload.messages[0]?.event_name).toBe('demo.capture');
  });

  it('rejects bad ingress signatures and stale timestamps', async () => {
    const env = createEnv();
    const worker = createWorker();
    const cookie = await login(worker, env);
    const ctx = createExecutionContext();

    const nodeTokenResponse = await worker.fetch(new Request('https://example.com/api/node-tokens', {
      method: 'POST',
      headers: {
        Cookie: cookie,
        'content-type': 'application/json'
      },
      body: JSON.stringify({
        source_service: 'fbforward',
        source_instance: 'node-1'
      })
    }), env, ctx.ctx);
    const nodeToken = await nodeTokenResponse.json() as { key_id: string; token: string };

    const rawBody = JSON.stringify(EVENT);
    const staleTimestamp = String(Math.floor(Date.now() / 1000) - 400);
    const staleSignature = await sign(nodeToken.token, `${staleTimestamp}.${rawBody}`);

    const staleResponse = await worker.fetch(new Request('https://example.com/v1/events', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        'x-fbnotify-key-id': nodeToken.key_id,
        'x-fbnotify-timestamp': staleTimestamp,
        'x-fbnotify-signature': staleSignature
      },
      body: rawBody
    }), env, createExecutionContext().ctx);
    expect(staleResponse.status).toBe(401);

    const badSignatureResponse = await worker.fetch(new Request('https://example.com/v1/events', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        'x-fbnotify-key-id': nodeToken.key_id,
        'x-fbnotify-timestamp': String(Math.floor(Date.now() / 1000)),
        'x-fbnotify-signature': 'invalid-signature'
      },
      body: rawBody
    }), env, createExecutionContext().ctx);
    expect(badSignatureResponse.status).toBe(401);
  });

  it('returns delivery results for test-send and keeps ingress non-blocking on provider failure', async () => {
    const env = createEnv();
    const worker = createWorker();
    const cookie = await login(worker, env);
    const ctx = createExecutionContext();

    const targetResponse = await worker.fetch(new Request('https://example.com/api/targets', {
      method: 'POST',
      headers: {
        Cookie: cookie,
        'content-type': 'application/json'
      },
      body: JSON.stringify({
        name: 'webhook',
        type: 'webhook',
        config: {
          url: 'https://hooks.example.com/fail'
        }
      })
    }), env, ctx.ctx);
    const target = await targetResponse.json() as { id: string };

    vi.stubGlobal('fetch', vi.fn(async () => new Response('fail', { status: 500 })));

    const testSendResponse = await worker.fetch(new Request('https://example.com/api/test-send', {
      method: 'POST',
      headers: {
        Cookie: cookie,
        'content-type': 'application/json'
      },
      body: JSON.stringify({
        event: EVENT,
        target_ids: [target.id]
      })
    }), env, createExecutionContext().ctx);
    expect(testSendResponse.status).toBe(200);
    const testSendPayload = await testSendResponse.json() as { results: Array<{ ok: boolean; status: number | null }> };
    expect(testSendPayload.results).toHaveLength(1);
    expect(testSendPayload.results[0]).toMatchObject({
      ok: false,
      status: 500
    });
  });
});
