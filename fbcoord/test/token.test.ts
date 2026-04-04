import { describe, expect, it } from 'vitest';

import { TokenStore, validateSharedTokenFormat } from '../src/durable-objects/token';
import { MemoryStorage } from './support';

const BOOTSTRAP_TOKEN = 'bootstrap-token-abcdefghijklmnopqrstuvwxyz123456';
const ROTATED_TOKEN = 'rotated-token-abcdefghijklmnopqrstuvwxyz789012';

describe('TokenStore', () => {
  it('bootstraps from the worker secret and validates it', async () => {
    const store = new TokenStore(new MemoryStorage(), BOOTSTRAP_TOKEN, () => 1_000);

    await expect(store.validate(BOOTSTRAP_TOKEN)).resolves.toBe(true);
    await expect(store.info()).resolves.toEqual({
      masked_prefix: 'bootstra...',
      created_at: 1_000
    });
  });

  it('invalidates the old token after rotation', async () => {
    const store = new TokenStore(new MemoryStorage(), BOOTSTRAP_TOKEN, () => 2_000);
    await store.validate(BOOTSTRAP_TOKEN);

    await store.rotate({ token: ROTATED_TOKEN });

    await expect(store.validate(BOOTSTRAP_TOKEN)).resolves.toBe(false);
    await expect(store.validate(ROTATED_TOKEN)).resolves.toBe(true);
  });

  it('returns generated tokens once and keeps the session secret stable', async () => {
    const storage = new MemoryStorage();
    const store = new TokenStore(storage, BOOTSTRAP_TOKEN, () => 3_000);
    const originalSecret = await store.sessionSecret();

    const result = await store.rotate({ generate: true });

    expect(result.token).toBeDefined();
    expect(result.token?.length).toBeGreaterThanOrEqual(32);
    await expect(store.sessionSecret()).resolves.toBe(originalSecret);
  });
});

describe('validateSharedTokenFormat', () => {
  it('rejects weak placeholder tokens', () => {
    expect(validateSharedTokenFormat('change-me')).toContain('placeholder');
  });

  it('rejects short tokens', () => {
    expect(validateSharedTokenFormat('too-short')).toContain('at least 32');
  });
});
