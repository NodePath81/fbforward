import { describe, expect, it } from 'vitest';

import { ConnectionRateLimiter, parseNodeInboundMessage, PoolState } from '../src/durable-objects/pool';

class FakeConnection {
  closed = false;

  close(): void {
    this.closed = true;
  }

  send(): void {}
}

describe('PoolState', () => {
  it('replaces the prior connection on same-node reconnect', () => {
    const state = new PoolState();
    const first = new FakeConnection();
    const second = new FakeConnection();

    expect(state.registerConnection('node-1', first).previous).toBeUndefined();
    const replaced = state.registerConnection('node-1', second).previous;

    expect(replaced).toBe(first);
  });

  it('recomputes the pick when a stale node is evicted', () => {
    let now = 0;
    const state = new PoolState(() => now, 30_000);

    state.setPreferences('node-1', ['a', 'b'], null);
    state.setPreferences('node-2', ['b'], null);
    expect(state.currentPick()).toEqual({ version: 2, upstream: 'b' });

    now = 31_000;
    const changed = state.reapStaleNodes();

    expect(changed).toBe(true);
    expect(state.currentPick()).toEqual({ version: 3, upstream: null });
  });

  it('increments version only when the visible pick changes', () => {
    const state = new PoolState();

    expect(state.setPreferences('node-1', ['a', 'b'], null)).toBe(true);
    expect(state.currentPick()).toEqual({ version: 1, upstream: 'a' });

    expect(state.setPreferences('node-1', ['a', 'b'], null)).toBe(false);
    expect(state.currentPick()).toEqual({ version: 1, upstream: 'a' });

    expect(state.setPreferences('node-2', ['a'], null)).toBe(false);
    expect(state.currentPick()).toEqual({ version: 1, upstream: 'a' });

    expect(state.setPreferences('node-2', ['b'], null)).toBe(true);
    expect(state.currentPick()).toEqual({ version: 2, upstream: 'b' });
  });

  it('exposes node snapshots with connection timestamps', () => {
    let now = 1_000;
    const state = new PoolState(() => now);

    state.registerConnection('node-1', new FakeConnection());
    state.setPreferences('node-1', ['a', 'b'], 'a');

    now = 2_000;
    state.heartbeat('node-1');

    expect(state.nodeSnapshot()).toEqual([
      {
        node_id: 'node-1',
        upstreams: ['a', 'b'],
        active_upstream: 'a',
        last_seen: 2_000,
        connected_at: 1_000
      }
    ]);
  });

  it('resets connection age on reconnect', () => {
    let now = 1_000;
    const state = new PoolState(() => now);

    state.registerConnection('node-1', new FakeConnection());
    now = 4_000;
    state.registerConnection('node-1', new FakeConnection());

    expect(state.nodeSnapshot()[0]?.connected_at).toBe(4_000);
  });

  it('throttles repeated same-node replacement churn', () => {
    let now = 1_000;
    const state = new PoolState(() => now);
    const first = new FakeConnection();
    const second = new FakeConnection();
    const third = new FakeConnection();

    expect(state.registerConnection('node-1', first).throttled).toBe(false);
    expect(state.registerConnection('node-1', second).throttled).toBe(false);

    now = 2_000;
    expect(state.registerConnection('node-1', third).throttled).toBe(true);
  });
});

describe('parseNodeInboundMessage', () => {
  it('accepts a valid hello message', () => {
    expect(parseNodeInboundMessage({
      type: 'hello',
      pool: 'alpha',
      node_id: 'node-1'
    }, 'alpha')).toEqual({
      message: {
        type: 'hello',
        pool: 'alpha',
        node_id: 'node-1'
      },
      close: false
    });
  });

  it('rejects malformed preferences payloads', () => {
    const result = parseNodeInboundMessage({
      type: 'preferences',
      upstreams: ['ok', 123]
    }, 'alpha');

    expect(result.error).toContain('strings');
    expect(result.close).toBe(true);
  });

  it('rejects preferences whose active_upstream is not in upstreams', () => {
    const result = parseNodeInboundMessage({
      type: 'preferences',
      upstreams: ['a', 'b'],
      active_upstream: 'c'
    }, 'alpha');

    expect(result.error).toContain('active_upstream');
    expect(result.close).toBe(true);
  });
});

describe('ConnectionRateLimiter', () => {
  it('allows short bursts and then blocks floods', () => {
    let now = 0;
    const limiter = new ConnectionRateLimiter(() => now, 5_000, 3);

    expect(limiter.allow()).toBe(true);
    expect(limiter.allow()).toBe(true);
    expect(limiter.allow()).toBe(true);
    expect(limiter.allow()).toBe(false);

    now = 6_000;
    expect(limiter.allow()).toBe(true);
  });
});
