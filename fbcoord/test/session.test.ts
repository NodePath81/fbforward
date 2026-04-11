import { describe, expect, it } from 'vitest';

import { createSession, createSessionRecord, validateSession } from '../src/session';

const encoder = new TextEncoder();

function bytesToBase64Url(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '');
}

async function signPayload(payload: string, secret: string): Promise<string> {
  const key = await crypto.subtle.importKey(
    'raw',
    encoder.encode(secret),
    { name: 'HMAC', hash: 'SHA-256' },
    false,
    ['sign']
  );
  const signature = await crypto.subtle.sign('HMAC', key, encoder.encode(payload));
  return bytesToBase64Url(new Uint8Array(signature));
}

describe('session helpers', () => {
  it('creates and validates a session', async () => {
    const secret = 'session-secret-abcdefghijklmnopqrstuvwxyz123456';
    const token = await createSession(secret, 60, 1_000);

    await expect(validateSession(token, secret, 30_000)).resolves.toBe(true);
  });

  it('rejects expired sessions', async () => {
    const secret = 'session-secret-abcdefghijklmnopqrstuvwxyz123456';
    const token = await createSession(secret, 5, 1_000);

    await expect(validateSession(token, secret, 10_000)).resolves.toBe(false);
  });

  it('rejects tampered sessions', async () => {
    const secret = 'session-secret-abcdefghijklmnopqrstuvwxyz123456';
    const token = await createSession(secret, 60, 1_000);
    const [payload, signature] = token.split('.');
    const replacement = signature.startsWith('a') ? 'b' : 'a';
    const tampered = `${payload}.${replacement}${signature.slice(1)}`;

    await expect(validateSession(tampered, secret, 10_000)).resolves.toBe(false);
  });

  it('creates unique session identifiers for fresh logins at the same timestamp', async () => {
    const secret = 'session-secret-abcdefghijklmnopqrstuvwxyz123456';
    const first = await createSessionRecord(secret, 60, 1_000);
    const second = await createSessionRecord(secret, 60, 1_000);

    expect(first.sessionId).not.toBe(second.sessionId);
    expect(first.token).not.toBe(second.token);
    await expect(validateSession(first.token, secret, 10_000)).resolves.toBe(true);
    await expect(validateSession(second.token, secret, 10_000)).resolves.toBe(true);
  });

  it('accepts legacy sessions without a session identifier until they expire', async () => {
    const secret = 'session-secret-abcdefghijklmnopqrstuvwxyz123456';
    const payload = bytesToBase64Url(encoder.encode(JSON.stringify({ exp: 61 })));
    const token = `${payload}.${await signPayload(payload, secret)}`;

    await expect(validateSession(token, secret, 30_000)).resolves.toBe(true);
  });
});
