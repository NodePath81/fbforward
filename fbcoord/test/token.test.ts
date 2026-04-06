import { describe, expect, it } from 'vitest';

import { TokenStore, validateSharedTokenFormat } from '../src/durable-objects/token';
import { MemoryStorage } from './support';

const BOOTSTRAP_TOKEN = 'bootstrap-token-abcdefghijklmnopqrstuvwxyz123456';
const ROTATED_TOKEN = 'rotated-token-abcdefghijklmnopqrstuvwxyz789012';
const PEPPER = 'pepper-abcdefghijklmnopqrstuvwxyz1234567890';

describe('TokenStore', () => {
  it('bootstraps from the worker secret and validates it', async () => {
    const store = new TokenStore(new MemoryStorage(), BOOTSTRAP_TOKEN, PEPPER, () => 1_000);

    await expect(store.validate(BOOTSTRAP_TOKEN)).resolves.toBe(true);
    await expect(store.info()).resolves.toEqual({
      masked_prefix: 'bootstra...',
      created_at: 1_000
    });
  });

  it('invalidates the old token after rotation', async () => {
    const store = new TokenStore(new MemoryStorage(), BOOTSTRAP_TOKEN, PEPPER, () => 2_000);
    await store.validate(BOOTSTRAP_TOKEN);

    await store.rotate({ token: ROTATED_TOKEN });

    await expect(store.validate(BOOTSTRAP_TOKEN)).resolves.toBe(false);
    await expect(store.validate(ROTATED_TOKEN)).resolves.toBe(true);
  });

  it('returns generated tokens once and keeps the session secret stable', async () => {
    const storage = new MemoryStorage();
    const store = new TokenStore(storage, BOOTSTRAP_TOKEN, PEPPER, () => 3_000);
    const originalSecret = await store.sessionSecret();

    const result = await store.rotate({ generate: true });

    expect(result.token).toBeDefined();
    expect(result.token?.length).toBeGreaterThanOrEqual(32);
    await expect(store.sessionSecret()).resolves.toBe(originalSecret);
  });

  it('migrates legacy sha256 records on successful validation', async () => {
    const storage = new MemoryStorage();
    const store = new TokenStore(storage, BOOTSTRAP_TOKEN, PEPPER, () => 4_000);

    const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(BOOTSTRAP_TOKEN));
    const tokenHash = btoa(String.fromCharCode(...new Uint8Array(digest)))
      .replace(/\+/g, '-')
      .replace(/\//g, '_')
      .replace(/=+$/g, '');

    await storage.put('token_record', {
      tokenHash,
      maskedPrefix: 'bootstra...',
      createdAt: 1_000,
      sessionSecret: 'legacy-session-secret-abcdefghijklmnopqrstuvwxyz'
    });

    await expect(store.validate(BOOTSTRAP_TOKEN)).resolves.toBe(true);
    const migrated = await storage.get<{ version?: string }>('token_record');
    expect(migrated?.version).toBe('pbkdf2-sha256-v1');
  });
});

describe('validateSharedTokenFormat', () => {
  it('rejects weak placeholder tokens', () => {
    expect(validateSharedTokenFormat('change-me')).toContain('placeholder');
  });

  it('rejects short tokens', () => {
    expect(validateSharedTokenFormat('too-short')).toContain('at least 32');
  });

  it('rejects repeated weak patterns', () => {
    expect(validateSharedTokenFormat('abc123abc123abc123abc123abc123ab')).not.toBeNull();
  });
});
