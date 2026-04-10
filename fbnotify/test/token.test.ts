import { describe, expect, it } from 'vitest';

import { TokenStore, validateSharedTokenFormat } from '../src/durable-objects/token';
import { MemoryStorage } from './support';

const BOOTSTRAP_TOKEN = 'bootstrap-token-abcdefghijklmnopqrstuvwxyz123456';
const ROTATED_TOKEN = 'rotated-token-abcdefghijklmnopqrstuvwxyz789012';
const PEPPER = 'pepper-abcdefghijklmnopqrstuvwxyz1234567890';

function bytesToBase64Url(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '');
}

async function sign(secret: string, payload: string): Promise<string> {
  const key = await crypto.subtle.importKey(
    'raw',
    new TextEncoder().encode(secret),
    { name: 'HMAC', hash: 'SHA-256' },
    false,
    ['sign']
  );
  const signature = await crypto.subtle.sign('HMAC', key, new TextEncoder().encode(payload));
  return bytesToBase64Url(new Uint8Array(signature));
}

describe('TokenStore', () => {
  it('bootstraps and rotates the operator token while keeping the session secret stable', async () => {
    const store = new TokenStore(new MemoryStorage(), BOOTSTRAP_TOKEN, PEPPER, () => 1_000);

    await expect(store.validateOperator(BOOTSTRAP_TOKEN)).resolves.toBe(true);
    await expect(store.info()).resolves.toEqual({
      masked_prefix: 'bootstra...',
      created_at: 1_000
    });

    const originalSessionSecret = await store.sessionSecret();
    const rotated = await store.rotate({ token: ROTATED_TOKEN });
    expect(rotated.token).toBeUndefined();
    await expect(store.validateOperator(BOOTSTRAP_TOKEN)).resolves.toBe(false);
    await expect(store.validateOperator(ROTATED_TOKEN)).resolves.toBe(true);
    await expect(store.sessionSecret()).resolves.toBe(originalSessionSecret);
  });

  it('creates, validates, rejects mismatched sources, and revokes node tokens', async () => {
    let now = 10_000;
    const store = new TokenStore(new MemoryStorage(), BOOTSTRAP_TOKEN, PEPPER, () => now);
    const created = await store.createNodeToken('fbforward', 'node-1');

    await expect(store.listNodeTokens()).resolves.toEqual([
      {
        key_id: created.key_id,
        source_service: 'fbforward',
        source_instance: 'node-1',
        masked_prefix: `${created.token.slice(0, 8)}...`,
        created_at: 10_000,
        last_used_at: null
      }
    ]);

    const timestamp = String(Math.floor(now / 1000));
    const rawBody = JSON.stringify({
      schema_version: 1,
      event_name: 'demo.test',
      severity: 'info',
      timestamp: now,
      source: {
        service: 'fbforward',
        instance: 'node-1'
      },
      attributes: {
        ok: true
      }
    });
    const signature = await sign(created.token, `${timestamp}.${rawBody}`);
    now = 12_000;
    await expect(store.validateIngress({
      key_id: created.key_id,
      timestamp,
      signature,
      raw_body: rawBody,
      source_service: 'fbforward',
      source_instance: 'node-1'
    })).resolves.toEqual({ valid: true });

    await expect(store.validateIngress({
      key_id: created.key_id,
      timestamp,
      signature,
      raw_body: rawBody,
      source_service: 'fbcoord',
      source_instance: 'node-1'
    })).resolves.toEqual({
      valid: false,
      error: 'source mismatch'
    });

    await store.revokeNodeToken(created.key_id);
    await expect(store.listNodeTokens()).resolves.toEqual([]);
  });
});

describe('validateSharedTokenFormat', () => {
  it('rejects weak tokens', () => {
    expect(validateSharedTokenFormat('change-me')).toContain('placeholder');
    expect(validateSharedTokenFormat('short')).toContain('at least 32');
  });
});
