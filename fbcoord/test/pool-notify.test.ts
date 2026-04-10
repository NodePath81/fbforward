import { afterEach, describe, expect, it, vi } from 'vitest';

import { PoolDurableObject } from '../src/durable-objects/pool';
import { createDurableObjectState, MemoryStorage } from './support';

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('PoolDurableObject notifications', () => {
  it('emits a notification when load normalization aborts an online node', async () => {
    const storage = new MemoryStorage();
    await storage.put('roster', {
      'node-1': {
        status: 'online',
        firstSeenAt: 1_000,
        lastConnectedAt: 2_000,
        lastSeenAt: 3_000,
        disconnectedAt: null
      }
    });
    const { state, flush } = createDurableObjectState(storage);
    const fetchMock = vi.fn(async () => new Response('ok', { status: 202 }));
    vi.stubGlobal('fetch', fetchMock);

    const pool = new PoolDurableObject(state, {
      FBNOTIFY_URL: 'https://notify.example/v1/events',
      FBNOTIFY_KEY_ID: 'notify-key',
      FBNOTIFY_TOKEN: 'notify-token-abcdefghijklmnopqrstuvwxyz123456',
      FBNOTIFY_SOURCE_INSTANCE: 'coord-1'
    });

    const response = await pool.fetch(new Request('https://pool.internal/state'));
    expect(response.status).toBe(200);
    await flush();

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const call = fetchMock.mock.calls[0] as unknown[] | undefined;
    expect(call).toBeDefined();
    const init = call?.[1] as RequestInit;
    expect(JSON.parse(String(init.body))).toMatchObject({
      event_name: 'pool.node_aborted',
      severity: 'warn',
      source: {
        service: 'fbcoord',
        instance: 'coord-1'
      },
      attributes: {
        'pool.name': 'global',
        'node.id': 'node-1',
        cause: 'load-normalization'
      }
    });
  });
});
