import { describe, expect, it } from 'vitest';

import { AuthGuardStore, activeBanKey } from '../src/durable-objects/auth-guard';
import { MemoryKV, MemoryStorage } from './support';

describe('AuthGuardStore', () => {
  it('blocks after repeated failures and writes a ban marker', async () => {
    let now = 0;
    const kv = new MemoryKV(() => now);
    const store = new AuthGuardStore(new MemoryStorage(), kv, () => now);

    await expect(store.recordFailure('login', '203.0.113.8')).resolves.toMatchObject({ blocked: false });
    await expect(store.recordFailure('login', '203.0.113.8')).resolves.toMatchObject({ blocked: false });
    await expect(store.recordFailure('login', '203.0.113.8')).resolves.toMatchObject({ blocked: true });

    await expect(kv.get<{ blocked_until: number }>(activeBanKey('login', '203.0.113.8'), 'json'))
      .resolves.toMatchObject({ blocked_until: 15 * 60_000 });
  });

  it('escalates repeated abuse inside the cleanout window', async () => {
    let now = 0;
    const kv = new MemoryKV(() => now);
    const store = new AuthGuardStore(new MemoryStorage(), kv, () => now);

    await store.recordFailure('login', '203.0.113.8');
    await store.recordFailure('login', '203.0.113.8');
    const first = await store.recordFailure('login', '203.0.113.8');
    expect(first.retry_after_seconds).toBe(900);

    now = 16 * 60_000;
    await store.recordFailure('login', '203.0.113.8');
    await store.recordFailure('login', '203.0.113.8');
    const second = await store.recordFailure('login', '203.0.113.8');
    expect(second.retry_after_seconds).toBe(1800);
    expect(second.penalty_level).toBe(1);
  });

  it('reduces only the matching scope history on success', async () => {
    let now = 0;
    const kv = new MemoryKV(() => now);
    const loginStore = new AuthGuardStore(new MemoryStorage(), kv, () => now);
    const nodeStore = new AuthGuardStore(new MemoryStorage(), kv, () => now);

    await loginStore.recordFailure('login', '203.0.113.8');
    await loginStore.recordFailure('login', '203.0.113.8');
    await nodeStore.recordFailure('node-auth', '203.0.113.8');
    await nodeStore.recordSuccess('node-auth', '203.0.113.8');

    await expect(loginStore.recordFailure('login', '203.0.113.8')).resolves.toMatchObject({ blocked: true });
  });
});
