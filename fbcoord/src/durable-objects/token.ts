const TOKEN_RECORD_KEY = 'token_record';
const MASK_PREFIX_LENGTH = 8;
const MIN_TOKEN_LENGTH = 32;

export interface TokenRecord {
  tokenHash: string;
  maskedPrefix: string;
  createdAt: number;
  sessionSecret: string;
}

export interface TokenInfo {
  masked_prefix: string;
  created_at: number;
}

interface ValidateBody {
  token?: string;
}

interface RotateBody {
  token?: string;
  generate?: boolean;
}

const encoder = new TextEncoder();

function bytesToBase64Url(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '');
}

function randomToken(): string {
  const bytes = new Uint8Array(32);
  crypto.getRandomValues(bytes);
  return bytesToBase64Url(bytes);
}

async function sha256(value: string): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', encoder.encode(value));
  return bytesToBase64Url(new Uint8Array(digest));
}

function maskToken(token: string): string {
  return `${token.slice(0, MASK_PREFIX_LENGTH)}...`;
}

export function validateSharedTokenFormat(token: string): string | null {
  const value = token.trim();
  if (value.toLowerCase() === 'change-me') {
    return 'token must not use the default placeholder value';
  }
  if (value.length < MIN_TOKEN_LENGTH) {
    return `token must be at least ${MIN_TOKEN_LENGTH} characters`;
  }
  return null;
}

function json(data: unknown, status: number = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: {
      'content-type': 'application/json; charset=utf-8'
    }
  });
}

export class TokenStore {
  constructor(
    private readonly storage: DurableObjectStorage,
    private readonly bootstrapToken: string,
    private readonly now: () => number = () => Date.now()
  ) {}

  async validate(candidate: string): Promise<boolean> {
    const record = await this.ensureRecord();
    const candidateHash = await sha256(candidate.trim());
    return candidateHash === record.tokenHash;
  }

  async info(): Promise<TokenInfo> {
    const record = await this.ensureRecord();
    return {
      masked_prefix: record.maskedPrefix,
      created_at: record.createdAt
    };
  }

  async sessionSecret(): Promise<string> {
    const record = await this.ensureRecord();
    return record.sessionSecret;
  }

  async rotate(body: RotateBody): Promise<{ info: TokenInfo; token?: string }> {
    const nextToken = body.generate ? randomToken() : body.token?.trim() ?? '';
    const error = validateSharedTokenFormat(nextToken);
    if (error) {
      throw new Error(error);
    }

    const current = await this.ensureRecord();
    const record: TokenRecord = {
      tokenHash: await sha256(nextToken),
      maskedPrefix: maskToken(nextToken),
      createdAt: this.now(),
      sessionSecret: current.sessionSecret
    };
    await this.storage.put(TOKEN_RECORD_KEY, record);

    return {
      info: {
        masked_prefix: record.maskedPrefix,
        created_at: record.createdAt
      },
      token: body.generate ? nextToken : undefined
    };
  }

  private async ensureRecord(): Promise<TokenRecord> {
    const existing = await this.storage.get<TokenRecord>(TOKEN_RECORD_KEY);
    if (existing) {
      return existing;
    }

    const bootstrap = this.bootstrapToken.trim();
    const error = validateSharedTokenFormat(bootstrap);
    if (error) {
      throw new Error(`FBCOORD_TOKEN ${error}`);
    }

    const record: TokenRecord = {
      tokenHash: await sha256(bootstrap),
      maskedPrefix: maskToken(bootstrap),
      createdAt: this.now(),
      sessionSecret: randomToken()
    };
    await this.storage.put(TOKEN_RECORD_KEY, record);
    return record;
  }
}

export class TokenDurableObject {
  private readonly store: TokenStore;

  constructor(state: DurableObjectState, env: { FBCOORD_TOKEN: string }) {
    this.store = new TokenStore(state.storage, env.FBCOORD_TOKEN);
  }

  async fetch(request: Request): Promise<Response> {
    const url = new URL(request.url);

    if (request.method === 'POST' && url.pathname === '/validate') {
      const body = await request.json() as ValidateBody;
      const token = body.token?.trim();
      if (!token) {
        return json({ valid: false }, 400);
      }
      return json({ valid: await this.store.validate(token) });
    }

    if (request.method === 'GET' && url.pathname === '/info') {
      return json(await this.store.info());
    }

    if (request.method === 'GET' && url.pathname === '/session-secret') {
      return json({ session_secret: await this.store.sessionSecret() });
    }

    if (request.method === 'POST' && url.pathname === '/rotate') {
      try {
        return json(await this.store.rotate(await request.json() as RotateBody));
      } catch (error) {
        return json({ error: error instanceof Error ? error.message : 'invalid token' }, 400);
      }
    }

    return new Response('not found', { status: 404 });
  }
}
