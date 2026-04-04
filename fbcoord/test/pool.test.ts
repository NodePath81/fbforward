import { describe, expect, it } from 'vitest';

import { PoolState } from '../src/durable-objects/pool';

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
});
