import { afterEach, describe, expect, it, vi } from 'vitest';

import { createNotifier } from '../src/notify';

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

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('createNotifier', () => {
  it('signs fbnotify payloads with timestamp plus raw body', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-04-10T00:00:00Z'));

    const fetchMock = vi.fn(async () => new Response('ok', { status: 202 }));
    const notifier = createNotifier({
      FBNOTIFY_URL: 'https://notify.example/v1/events',
      FBNOTIFY_KEY_ID: 'key-1',
      FBNOTIFY_TOKEN: 'node-token-abcdefghijklmnopqrstuvwxyz123456',
      FBNOTIFY_SOURCE_INSTANCE: 'coord-1'
    }, 'fbcoord', fetchMock as typeof fetch);

    await notifier.send('operator.login', 'info', {
      'client.ip': '203.0.113.8'
    });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const call = fetchMock.mock.calls[0] as unknown[] | undefined;
    expect(call).toBeDefined();
    const url = call?.[0] as string;
    const init = call?.[1] as RequestInit;
    expect(url).toBe('https://notify.example/v1/events');
    expect(init.method).toBe('POST');
    const rawBody = String(init.body);
    const headers = new Headers(init.headers);
    expect(headers.get('x-fbnotify-key-id')).toBe('key-1');
    expect(headers.get('x-fbnotify-timestamp')).toBe('1775779200');
    const expectedSignature = await sign(
      'node-token-abcdefghijklmnopqrstuvwxyz123456',
      `${headers.get('x-fbnotify-timestamp')}.${rawBody}`
    );
    expect(headers.get('x-fbnotify-signature')).toBe(expectedSignature);
    expect(JSON.parse(rawBody)).toEqual({
      schema_version: 1,
      event_name: 'operator.login',
      severity: 'info',
      timestamp: '2026-04-10T00:00:00.000Z',
      source: {
        service: 'fbcoord',
        instance: 'coord-1'
      },
      attributes: {
        'client.ip': '203.0.113.8'
      }
    });
  });

  it('is a no-op when fbnotify env is incomplete', async () => {
    const fetchMock = vi.fn();
    const notifier = createNotifier({
      FBNOTIFY_URL: 'https://notify.example/v1/events',
      FBNOTIFY_KEY_ID: 'key-1',
      FBNOTIFY_TOKEN: '',
      FBNOTIFY_SOURCE_INSTANCE: 'coord-1'
    }, 'fbcoord', fetchMock as typeof fetch);

    await expect(notifier.status()).resolves.toEqual({
      configured: false,
      source: 'none',
      endpoint: 'https://notify.example/v1/events',
      key_id: 'key-1',
      source_instance: 'coord-1',
      masked_prefix: '',
      updated_at: null,
      missing: ['token']
    });
    await notifier.send('operator.login', 'info');

    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('logs the bounded response body when fbnotify rejects delivery', async () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    const notifier = createNotifier({
      FBNOTIFY_URL: 'https://notify.example/v1/events',
      FBNOTIFY_KEY_ID: 'key-1',
      FBNOTIFY_TOKEN: 'node-token-abcdefghijklmnopqrstuvwxyz123456',
      FBNOTIFY_SOURCE_INSTANCE: 'coord-1'
    }, 'fbcoord', vi.fn(async () => new Response('signature mismatch', { status: 401 })) as typeof fetch);

    await notifier.send('operator.login', 'info');

    expect(warnSpy).toHaveBeenCalledWith(
      'fbcoord notification',
      expect.objectContaining({
        action: 'delivery_failed',
        http_status: 401,
        response_body: 'signature mismatch'
      })
    );
  });
});
