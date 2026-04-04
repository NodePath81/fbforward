import { describe, expect, it } from 'vitest';

import { RegistryStore } from '../src/durable-objects/registry';
import { MemoryStorage } from './support';

describe('RegistryStore', () => {
  it('registers and lists pools in sorted order', async () => {
    const store = new RegistryStore(new MemoryStorage());

    await store.register('pool-b');
    await store.register('pool-a');

    await expect(store.list()).resolves.toEqual(['pool-a', 'pool-b']);
  });

  it('deregisters pools cleanly', async () => {
    const store = new RegistryStore(new MemoryStorage());

    await store.register('pool-a');
    await store.deregister('pool-a');

    await expect(store.list()).resolves.toEqual([]);
  });
});
