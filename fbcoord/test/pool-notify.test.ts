import { afterEach, describe, expect, it, vi } from 'vitest';

import { PoolDurableObject } from '../src/durable-objects/pool';
import { createDurableObjectState, MemoryStorage } from './support';

class FakeSocket {
  private readonly listeners = new Map<string, Array<(event?: { data?: unknown }) => void>>();

  addEventListener(type: string, handler: (event?: { data?: unknown }) => void): void {
    const current = this.listeners.get(type) ?? [];
    current.push(handler);
    this.listeners.set(type, current);
  }

  accept(): void {}

  send(): void {}

  close(): void {
    for (const handler of this.listeners.get('close') ?? []) {
      handler();
    }
  }

  emitMessage(data: unknown): void {
    for (const handler of this.listeners.get('message') ?? []) {
      handler({ data });
    }
  }
}

const BASE_TIME = new Date('2026-04-10T00:00:00Z');
const DEFAULT_NOTIFY_ENV = {
  FBNOTIFY_URL: 'https://notify.example/v1/events',
  FBNOTIFY_KEY_ID: 'notify-key',
  FBNOTIFY_TOKEN: 'notify-token-abcdefghijklmnopqrstuvwxyz123456',
  FBNOTIFY_SOURCE_INSTANCE: 'coord-1'
};

function createPool(
  storage: MemoryStorage,
  overrides: Partial<{
    FBNOTIFY_URL: string;
    FBNOTIFY_KEY_ID: string;
    FBNOTIFY_TOKEN: string;
    FBNOTIFY_SOURCE_INSTANCE: string;
    FBCOORD_ABORTED_NOTIFY_DELAY_MS: string;
  }> = {},
  fetchImpl: typeof fetch = fetch
): {
  pool: PoolDurableObject;
  state: {
    ensureLoaded(): Promise<void>;
    persistStateIfNeeded(rosterChanged: boolean, pendingChanged: boolean): Promise<void>;
    syncAlarm(): Promise<void>;
    state: {
      registerConnection(nodeId: string, connection: WebSocket): { rosterChanged: boolean };
    };
  };
  flush(): Promise<void>;
} {
  const { state, flush } = createDurableObjectState(storage);
  vi.stubGlobal('fetch', fetchImpl);
  const pool = new PoolDurableObject(state, {
    ...DEFAULT_NOTIFY_ENV,
    ...overrides
  });
  return {
    pool,
    state: pool as unknown as {
      ensureLoaded(): Promise<void>;
      persistStateIfNeeded(rosterChanged: boolean, pendingChanged: boolean): Promise<void>;
      syncAlarm(): Promise<void>;
      state: {
        registerConnection(nodeId: string, connection: WebSocket): { rosterChanged: boolean };
      };
    },
    flush
  };
}

function notificationBody(fetchMock: ReturnType<typeof vi.fn>, index: number = 0): Record<string, unknown> {
  const call = fetchMock.mock.calls[index];
  expect(call).toBeDefined();
  const init = call?.[1] as RequestInit;
  return JSON.parse(String(init.body)) as Record<string, unknown>;
}

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('PoolDurableObject notifications', () => {
  it('delays load-normalization notifications by the default 30 seconds', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(BASE_TIME);

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
    const fetchMock = vi.fn(async () => new Response('ok', { status: 202 }));
    const { pool, flush } = createPool(storage, {}, fetchMock);

    const response = await pool.fetch(new Request('https://pool.internal/state'));
    expect(response.status).toBe(200);
    await flush();

    expect(fetchMock).toHaveBeenCalledTimes(0);
    expect(await storage.getAlarm()).toBe(BASE_TIME.getTime() + 30_000);

    vi.setSystemTime(new Date(BASE_TIME.getTime() + 30_000));
    await pool.alarm();
    await flush();

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(notificationBody(fetchMock)).toMatchObject({
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
    expect(await storage.getAlarm()).toBeNull();
  });

  it('delays timeout notifications until the configured alarm delay elapses', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(BASE_TIME);

    const storage = new MemoryStorage();
    const fetchMock = vi.fn(async () => new Response('ok', { status: 202 }));
    const { pool, state, flush } = createPool(storage, {
      FBCOORD_ABORTED_NOTIFY_DELAY_MS: '1000'
    }, fetchMock);

    await state.ensureLoaded();
    const socket = new FakeSocket();
    const result = state.state.registerConnection('node-1', socket as unknown as WebSocket);
    await state.persistStateIfNeeded(result.rosterChanged, false);
    await state.syncAlarm();
    await flush();

    expect(await storage.getAlarm()).toBe(BASE_TIME.getTime() + 30_000);

    vi.setSystemTime(new Date(BASE_TIME.getTime() + 30_001));
    await pool.alarm();
    await flush();

    expect(fetchMock).toHaveBeenCalledTimes(0);
    expect(await storage.getAlarm()).toBe(BASE_TIME.getTime() + 31_001);

    vi.setSystemTime(new Date(BASE_TIME.getTime() + 31_001));
    await pool.alarm();
    await flush();

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(notificationBody(fetchMock)).toMatchObject({
      event_name: 'pool.node_aborted',
      severity: 'warn',
      attributes: {
        'node.id': 'node-1',
        cause: 'timeout'
      }
    });
    expect(await storage.getAlarm()).toBeNull();
  });

  it('drops pending aborted notifications when a node reconnects before the delay expires', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(BASE_TIME);

    const storage = new MemoryStorage();
    const fetchMock = vi.fn(async () => new Response('ok', { status: 202 }));
    const { pool, state, flush } = createPool(storage, {
      FBCOORD_ABORTED_NOTIFY_DELAY_MS: '1000'
    }, fetchMock);

    await state.ensureLoaded();
    const firstSocket = new FakeSocket();
    const firstResult = state.state.registerConnection('node-1', firstSocket as unknown as WebSocket);
    await state.persistStateIfNeeded(firstResult.rosterChanged, false);
    await state.syncAlarm();
    await flush();

    vi.setSystemTime(new Date(BASE_TIME.getTime() + 30_001));
    await pool.alarm();
    await flush();

    expect(fetchMock).toHaveBeenCalledTimes(0);
    expect(await storage.getAlarm()).toBe(BASE_TIME.getTime() + 31_001);

    vi.setSystemTime(new Date(BASE_TIME.getTime() + 30_500));
    const secondSocket = new FakeSocket();
    const secondResult = state.state.registerConnection('node-1', secondSocket as unknown as WebSocket);
    await state.persistStateIfNeeded(secondResult.rosterChanged, false);
    await state.syncAlarm();
    await flush();

    expect(await storage.getAlarm()).toBe(BASE_TIME.getTime() + 60_500);

    vi.setSystemTime(new Date(BASE_TIME.getTime() + 31_001));
    await pool.alarm();
    await flush();

    expect(fetchMock).toHaveBeenCalledTimes(0);
  });

  it('sends immediately when the aborted notification delay is zero', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(BASE_TIME);

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
    const fetchMock = vi.fn(async () => new Response('ok', { status: 202 }));
    const { pool, flush } = createPool(storage, {
      FBCOORD_ABORTED_NOTIFY_DELAY_MS: '0'
    }, fetchMock);

    const response = await pool.fetch(new Request('https://pool.internal/state'));
    expect(response.status).toBe(200);
    await flush();

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(notificationBody(fetchMock)).toMatchObject({
      event_name: 'pool.node_aborted',
      attributes: {
        'node.id': 'node-1',
        cause: 'load-normalization'
      }
    });
  });
});
