import { describe, expect, it } from 'vitest';

import { createSession, validateSession } from '../src/session';

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
    const tampered = `${token.slice(0, -1)}x`;

    await expect(validateSession(tampered, secret, 10_000)).resolves.toBe(false);
  });
});
