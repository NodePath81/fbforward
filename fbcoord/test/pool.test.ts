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

  it('recomputes the pick when stale nodes are evicted', () => {
    let now = 0;
    const state = new PoolState(() => now, 30_000);
    const first = new FakeConnection();
    const second = new FakeConnection();

    state.registerConnection('node-1', first);
    state.registerConnection('node-2', second);
    state.setPreferences('node-1', first, ['a', 'b'], null);
    state.setPreferences('node-2', second, ['b'], null);
    expect(state.currentPick()).toEqual({ version: 1, upstream: 'b' });

    now = 31_000;
    const result = state.reapStaleNodes();

    expect(result.changed).toBe(true);
    expect(result.connectionsToClose).toEqual([first, second]);
    expect(result.aborted_nodes).toEqual([
      { node_id: 'node-1', cause: 'timeout' },
      { node_id: 'node-2', cause: 'timeout' }
    ]);
    expect(state.currentPick()).toEqual({ version: 2, upstream: null });
  });

  it('increments version only when the visible pick changes', () => {
    const state = new PoolState();
    const first = new FakeConnection();
    const second = new FakeConnection();

    state.registerConnection('node-1', first);
    expect(state.setPreferences('node-1', first, ['a', 'b'], null).changed).toBe(true);
    expect(state.currentPick()).toEqual({ version: 1, upstream: 'a' });

    expect(state.setPreferences('node-1', first, ['a', 'b'], null).changed).toBe(false);
    expect(state.currentPick()).toEqual({ version: 1, upstream: 'a' });

    expect(state.registerConnection('node-2', second).changed).toBe(true);
    expect(state.currentPick()).toEqual({ version: 2, upstream: null });

    expect(state.setPreferences('node-2', second, ['a'], null).changed).toBe(true);
    expect(state.currentPick()).toEqual({ version: 3, upstream: 'a' });

    expect(state.setPreferences('node-2', second, ['a'], null).changed).toBe(false);
    expect(state.currentPick()).toEqual({ version: 3, upstream: 'a' });

    expect(state.setPreferences('node-2', second, ['b'], null).changed).toBe(true);
    expect(state.currentPick()).toEqual({ version: 4, upstream: 'b' });
  });

  it('exposes roster snapshots with status and timestamps', () => {
    let now = 1_000;
    const state = new PoolState(() => now);
    const connection = new FakeConnection();

    state.registerConnection('node-1', connection);
    state.setPreferences('node-1', connection, ['a', 'b'], 'a');

    now = 2_000;
    state.heartbeat('node-1', connection);

    expect(state.stateSnapshot()).toEqual({
      pick: { version: 1, upstream: 'a' },
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
          first_seen_at: 1_000,
          last_connected_at: 1_000,
          last_seen_at: 2_000,
          disconnected_at: null,
          upstreams: ['a', 'b'],
          active_upstream: 'a'
        }
      ]
    });
  });

  it('records offline teardown without exposing active upstreams afterward', () => {
    let now = 1_000;
    const state = new PoolState(() => now);
    const connection = new FakeConnection();

    state.registerConnection('node-1', connection);
    state.setPreferences('node-1', connection, ['a'], 'a');

    now = 3_000;
    expect(state.acceptTeardown('node-1', connection)).toEqual({
      changed: true,
      rosterChanged: true,
      aborted_nodes: []
    });

    expect(state.stateSnapshot().nodes).toEqual([
      {
        node_id: 'node-1',
        status: 'offline',
        first_seen_at: 1_000,
        last_connected_at: 1_000,
        last_seen_at: 1_000,
        disconnected_at: 3_000,
        upstreams: [],
        active_upstream: null
      }
    ]);
  });

  it('marks abrupt disconnects as aborted and preserves prior timestamps', () => {
    let now = 1_000;
    const state = new PoolState(() => now);
    const connection = new FakeConnection();

    state.registerConnection('node-1', connection);
    now = 2_000;
    state.heartbeat('node-1', connection);

    now = 4_000;
    expect(state.abortConnection('node-1', connection)).toEqual({
      changed: false,
      rosterChanged: true,
      aborted_nodes: [{
        node_id: 'node-1',
        cause: 'disconnect'
      }]
    });

    expect(state.stateSnapshot().nodes[0]).toEqual({
      node_id: 'node-1',
      status: 'aborted',
      first_seen_at: 1_000,
      last_connected_at: 1_000,
      last_seen_at: 2_000,
      disconnected_at: 4_000,
      upstreams: [],
      active_upstream: null
    });
  });

  it('resets last_connected_at on reconnect while preserving first_seen_at', () => {
    let now = 1_000;
    const state = new PoolState(() => now);
    const first = new FakeConnection();
    const second = new FakeConnection();

    state.registerConnection('node-1', first);
    now = 4_000;
    state.abortConnection('node-1', first);

    now = 9_000;
    state.registerConnection('node-1', second);

    expect(state.stateSnapshot().nodes[0]).toEqual({
      node_id: 'node-1',
      status: 'online',
      first_seen_at: 1_000,
      last_connected_at: 9_000,
      last_seen_at: 9_000,
      disconnected_at: null,
      upstreams: [],
      active_upstream: null
    });
  });

  it('normalizes persisted online entries to aborted on load', () => {
    const state = new PoolState(() => 7_000);

    state.hydrateRoster({
      'node-1': {
        status: 'online',
        firstSeenAt: 1_000,
        lastConnectedAt: 2_000,
        lastSeenAt: 3_000,
        disconnectedAt: null
      }
    });

    expect(state.normalizeLoadedRoster()).toEqual([{
      node_id: 'node-1',
      cause: 'load-normalization'
    }]);
    expect(state.stateSnapshot().nodes[0]).toEqual({
      node_id: 'node-1',
      status: 'aborted',
      first_seen_at: 1_000,
      last_connected_at: 2_000,
      last_seen_at: 3_000,
      disconnected_at: 7_000,
      upstreams: [],
      active_upstream: null
    });
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
  it('accepts a minimal hello message', () => {
    expect(parseNodeInboundMessage({
      type: 'hello'
    })).toEqual({
      message: {
        type: 'hello'
      },
      close: false
    });
  });

  it('accepts a bye message', () => {
    expect(parseNodeInboundMessage({
      type: 'bye'
    })).toEqual({
      message: {
        type: 'bye'
      },
      close: false
    });
  });

  it('ignores legacy pool and node_id fields on hello', () => {
    expect(parseNodeInboundMessage({
      type: 'hello',
      pool: 'alpha',
      node_id: 'node-1'
    })).toEqual({
      message: {
        type: 'hello'
      },
      close: false
    });
  });

  it('rejects malformed preferences payloads', () => {
    const result = parseNodeInboundMessage({
      type: 'preferences',
      upstreams: ['ok', 123]
    });

    expect(result.error).toContain('strings');
    expect(result.close).toBe(true);
  });

  it('rejects preferences whose active_upstream is not in upstreams', () => {
    const result = parseNodeInboundMessage({
      type: 'preferences',
      upstreams: ['a', 'b'],
      active_upstream: 'c'
    });

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
