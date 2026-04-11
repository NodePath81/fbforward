const encoder = new TextEncoder();

export const SESSION_COOKIE_NAME = 'fbcoord_session';
export const SESSION_TTL_SECONDS = 24 * 60 * 60;

interface SessionPayload {
  exp: number;
  session_id?: string;
}

export interface CreatedSession {
  token: string;
  sessionId: string;
}

function bytesToBase64Url(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '');
}

function base64UrlToBytes(value: string): Uint8Array {
  const padding = value.length % 4 === 0 ? '' : '='.repeat(4 - (value.length % 4));
  const normalized = value.replace(/-/g, '+').replace(/_/g, '/') + padding;
  const binary = atob(normalized);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}

function constantTimeEqual(left: Uint8Array, right: Uint8Array): boolean {
  if (left.length !== right.length) {
    return false;
  }
  let diff = 0;
  for (let i = 0; i < left.length; i += 1) {
    diff |= left[i] ^ right[i];
  }
  return diff === 0;
}

async function importHmacKey(secret: string): Promise<CryptoKey> {
  return crypto.subtle.importKey(
    'raw',
    encoder.encode(secret),
    { name: 'HMAC', hash: 'SHA-256' },
    false,
    ['sign']
  );
}

async function signPayload(payload: string, secret: string): Promise<Uint8Array> {
  const key = await importHmacKey(secret);
  const signature = await crypto.subtle.sign('HMAC', key, encoder.encode(payload));
  return new Uint8Array(signature);
}

export async function createSession(
  secret: string,
  ttlSeconds: number = SESSION_TTL_SECONDS,
  nowMs: number = Date.now()
): Promise<string> {
  const session = await createSessionRecord(secret, ttlSeconds, nowMs);
  return session.token;
}

export async function createSessionRecord(
  secret: string,
  ttlSeconds: number = SESSION_TTL_SECONDS,
  nowMs: number = Date.now()
): Promise<CreatedSession> {
  const sessionId = crypto.randomUUID();
  const payload: SessionPayload = {
    exp: Math.floor(nowMs / 1000) + ttlSeconds,
    session_id: sessionId
  };
  const encodedPayload = bytesToBase64Url(encoder.encode(JSON.stringify(payload)));
  const encodedSignature = bytesToBase64Url(await signPayload(encodedPayload, secret));
  return {
    token: `${encodedPayload}.${encodedSignature}`,
    sessionId
  };
}

export async function validateSession(
  token: string,
  secret: string,
  nowMs: number = Date.now()
): Promise<boolean> {
  const [payloadPart, signaturePart, extra] = token.split('.');
  if (!payloadPart || !signaturePart || extra !== undefined) {
    return false;
  }

  let payload: SessionPayload;
  try {
    payload = JSON.parse(new TextDecoder().decode(base64UrlToBytes(payloadPart))) as SessionPayload;
  } catch {
    return false;
  }

  if (typeof payload.exp !== 'number' || payload.exp <= Math.floor(nowMs / 1000)) {
    return false;
  }
  if (payload.session_id !== undefined && typeof payload.session_id !== 'string') {
    return false;
  }

  let providedSignature: Uint8Array;
  try {
    providedSignature = base64UrlToBytes(signaturePart);
  } catch {
    return false;
  }

  const expectedSignature = await signPayload(payloadPart, secret);
  return constantTimeEqual(providedSignature, expectedSignature);
}

function buildCookieAttributes(ttlSeconds: number, secure: boolean): string[] {
  return [
    `Max-Age=${ttlSeconds}`,
    'HttpOnly',
    'Path=/',
    'SameSite=Strict',
    ...(secure ? ['Secure'] : [])
  ];
}

export function createSessionCookie(
  session: string,
  ttlSeconds?: number,
  secure: boolean = true
): string {
  const ttl = ttlSeconds ?? SESSION_TTL_SECONDS;
  return [
    `${SESSION_COOKIE_NAME}=${encodeURIComponent(session)}`,
    ...buildCookieAttributes(ttl, secure)
  ].join('; ');
}

export function clearSessionCookie(secure: boolean = true): string {
  return [
    `${SESSION_COOKIE_NAME}=`,
    ...buildCookieAttributes(0, secure)
  ].join('; ');
}
