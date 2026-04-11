import { describe, expect, it } from 'vitest';

import { TokenStore, validateSharedTokenFormat } from '../src/durable-objects/token';
import { MemoryStorage } from './support';

const BOOTSTRAP_TOKEN = 'bootstrap-token-abcdefghijklmnopqrstuvwxyz123456';
const ROTATED_TOKEN = 'rotated-token-abcdefghijklmnopqrstuvwxyz789012';
const PEPPER = 'pepper-abcdefghijklmnopqrstuvwxyz1234567890';

describe('TokenStore', () => {
  it('bootstraps the operator token from the worker secret and validates it', async () => {
    const store = new TokenStore(new MemoryStorage(), BOOTSTRAP_TOKEN, PEPPER, () => 1_000);

    await expect(store.validateOperator(BOOTSTRAP_TOKEN)).resolves.toBe(true);
    await expect(store.info()).resolves.toEqual({
      masked_prefix: 'bootstra...',
      created_at: 1_000
    });
  });

  it('invalidates the old operator token after rotation', async () => {
    const store = new TokenStore(new MemoryStorage(), BOOTSTRAP_TOKEN, PEPPER, () => 2_000);
    await store.validateOperator(BOOTSTRAP_TOKEN);

    await store.rotate({ token: ROTATED_TOKEN });

    await expect(store.validateOperator(BOOTSTRAP_TOKEN)).resolves.toBe(false);
    await expect(store.validateOperator(ROTATED_TOKEN)).resolves.toBe(true);
  });

  it('returns generated operator tokens once and keeps the session secret stable', async () => {
    const storage = new MemoryStorage();
    const store = new TokenStore(storage, BOOTSTRAP_TOKEN, PEPPER, () => 3_000);
    const originalSecret = await store.sessionSecret();

    const result = await store.rotate({ generate: true });

    expect(result.token).toBeDefined();
    expect(result.token?.length).toBeGreaterThanOrEqual(32);
    await expect(store.sessionSecret()).resolves.toBe(originalSecret);
  });

  it('migrates legacy sha256 operator records on successful validation', async () => {
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

    await expect(store.validateOperator(BOOTSTRAP_TOKEN)).resolves.toBe(true);
    const migrated = await storage.get<{ version?: string }>('token_record');
    expect(migrated?.version).toBe('pbkdf2-sha256-v1');
  });

  it('creates, lists, validates, and revokes node tokens', async () => {
    let now = 5_000;
    const store = new TokenStore(new MemoryStorage(), BOOTSTRAP_TOKEN, PEPPER, () => now);

    const created = await store.createNodeToken('node-1');
    expect(created.token.length).toBeGreaterThanOrEqual(32);
    expect(created.info).toEqual({
      node_id: 'node-1',
      masked_prefix: `${created.token.slice(0, 8)}...`,
      created_at: 5_000,
      last_used_at: null
    });

    await expect(store.listNodeTokens()).resolves.toEqual([created.info]);

    now = 6_000;
    await expect(store.validateNode(created.token)).resolves.toEqual({
      valid: true,
      node_id: 'node-1'
    });
    await expect(store.listNodeTokens()).resolves.toEqual([
      {
        ...created.info,
        last_used_at: 6_000
      }
    ]);

    await store.revokeNodeToken('node-1');
    await expect(store.validateNode(created.token)).resolves.toEqual({ valid: false });
    await expect(store.listNodeTokens()).resolves.toEqual([]);
  });

  it('rejects duplicate node IDs until the original token is revoked', async () => {
    const store = new TokenStore(new MemoryStorage(), BOOTSTRAP_TOKEN, PEPPER, () => 7_000);

    await store.createNodeToken('node-1');
    await expect(store.createNodeToken('node-1')).rejects.toThrow('node_id already exists');

    await store.revokeNodeToken('node-1');
    await expect(store.createNodeToken('node-1')).resolves.toMatchObject({
      info: {
        node_id: 'node-1'
      }
    });
  });

  it('keeps node tokens valid across operator token rotation', async () => {
    const store = new TokenStore(new MemoryStorage(), BOOTSTRAP_TOKEN, PEPPER, () => 8_000);
    const created = await store.createNodeToken('node-1');

    await store.rotate({ token: ROTATED_TOKEN });

    await expect(store.validateOperator(ROTATED_TOKEN)).resolves.toBe(true);
    await expect(store.validateNode(created.token)).resolves.toEqual({
      valid: true,
      node_id: 'node-1'
    });
  });

  it('bootstraps notify config from env and exposes masked metadata only', async () => {
    const store = new TokenStore(
      new MemoryStorage(),
      BOOTSTRAP_TOKEN,
      PEPPER,
      {
        endpoint: 'https://notify.example/v1/events',
        key_id: 'notify-key',
        token: 'notify-token-abcdefghijklmnopqrstuvwxyz123456',
        source_instance: 'coord-1',
      },
      () => 9_000
    );

    await expect(store.notifyConfigInfo()).resolves.toEqual({
      configured: true,
      source: 'bootstrap-env',
      endpoint: 'https://notify.example/v1/events',
      key_id: 'notify-key',
      source_instance: 'coord-1',
      masked_prefix: 'notify-t...',
      updated_at: 9_000,
      missing: []
    });
    await expect(store.internalNotifyConfig()).resolves.toEqual({
      configured: true,
      source: 'bootstrap-env',
      endpoint: 'https://notify.example/v1/events',
      key_id: 'notify-key',
      source_instance: 'coord-1',
      masked_prefix: 'notify-t...',
      updated_at: 9_000,
      missing: [],
      token: 'notify-token-abcdefghijklmnopqrstuvwxyz123456'
    });
  });

  it('stores and returns updated notify config independently from bootstrap env', async () => {
    const store = new TokenStore(
      new MemoryStorage(),
      BOOTSTRAP_TOKEN,
      PEPPER,
      {
        endpoint: 'https://notify.example/v1/events',
        key_id: 'bootstrap-key',
        token: 'bootstrap-token-abcdefghijklmnopqrstuvwxyz123456',
        source_instance: 'coord-bootstrap',
      },
      () => 10_000
    );

    await expect(store.updateNotifyConfig({
      endpoint: 'https://notify-2.example/v1/events',
      key_id: 'stored-key',
      token: 'stored-token-abcdefghijklmnopqrstuvwxyz123456',
      source_instance: 'coord-stored',
    })).resolves.toEqual({
      configured: true,
      source: 'stored',
      endpoint: 'https://notify-2.example/v1/events',
      key_id: 'stored-key',
      source_instance: 'coord-stored',
      masked_prefix: 'stored-t...',
      updated_at: 10_000,
      missing: []
    });

    await expect(store.internalNotifyConfig()).resolves.toEqual({
      configured: true,
      source: 'stored',
      endpoint: 'https://notify-2.example/v1/events',
      key_id: 'stored-key',
      source_instance: 'coord-stored',
      masked_prefix: 'stored-t...',
      updated_at: 10_000,
      missing: [],
      token: 'stored-token-abcdefghijklmnopqrstuvwxyz123456'
    });
  });

  it('reports missing notify-config fields when env bootstrap is incomplete', async () => {
    const store = new TokenStore(new MemoryStorage(), BOOTSTRAP_TOKEN, PEPPER, {}, () => 11_000);

    await expect(store.notifyConfigInfo()).resolves.toEqual({
      configured: false,
      source: 'none',
      endpoint: '',
      key_id: '',
      source_instance: '',
      masked_prefix: '',
      updated_at: null,
      missing: ['endpoint', 'key_id', 'token', 'source_instance']
    });
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
