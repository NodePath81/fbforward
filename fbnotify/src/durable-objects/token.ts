import type { CreateNodeTokenResponse, NodeTokenInfo, OperatorTokenInfo } from '../types';

const OPERATOR_RECORD_KEY = 'operator_record';
const NODE_TOKENS_KEY = 'node_tokens';
const SOURCE_LOOKUP_KEY = 'source_lookup';
const MASK_PREFIX_LENGTH = 8;
const MIN_TOKEN_LENGTH = 32;
const PBKDF2_ITERATIONS = 50_000;
const PBKDF2_VERSION = 'pbkdf2-sha256-v1';
const INGEST_MAX_SKEW_SECONDS = 300;

interface OperatorRecord {
  version: typeof PBKDF2_VERSION;
  iterations: number;
  salt: string;
  verifier: string;
  maskedPrefix: string;
  createdAt: number;
  sessionSecret: string;
}

interface NodeTokenRecord {
  keyId: string;
  sourceService: string;
  sourceInstance: string;
  secret: string;
  maskedPrefix: string;
  createdAt: number;
  lastUsedAt: number | null;
}

type NodeTokenRecords = Record<string, NodeTokenRecord>;
type SourceLookup = Record<string, string>;

interface ValidateIngressBody {
  key_id?: string;
  timestamp?: string;
  signature?: string;
  raw_body?: string;
  source_service?: string;
  source_instance?: string;
}

interface RotateBody {
  token?: string;
  generate?: boolean;
}

interface CreateNodeTokenBody {
  source_service?: string;
  source_instance?: string;
}

const encoder = new TextEncoder();

function json(data: unknown, status: number = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: {
      'content-type': 'application/json; charset=utf-8'
    }
  });
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

function randomToken(byteLength: number = 32): string {
  const bytes = new Uint8Array(byteLength);
  crypto.getRandomValues(bytes);
  return bytesToBase64Url(bytes);
}

function maskToken(token: string): string {
  return `${token.slice(0, MASK_PREFIX_LENGTH)}...`;
}

function repeatedPatternLength(value: string): number | null {
  for (let size = 1; size <= Math.min(12, Math.floor(value.length / 2)); size += 1) {
    if (value.length % size !== 0) {
      continue;
    }
    const chunk = value.slice(0, size);
    if (chunk.repeat(value.length / size) === value) {
      return size;
    }
  }
  return null;
}

export function validateSharedTokenFormat(token: string): string | null {
  const value = token.trim();
  if (value.toLowerCase() === 'change-me') {
    return 'token must not use the default placeholder value';
  }
  if (value.length < MIN_TOKEN_LENGTH) {
    return `token must be at least ${MIN_TOKEN_LENGTH} characters`;
  }

  const normalized = value.toLowerCase().replace(/[^a-z0-9]/g, '');
  if (!normalized) {
    return 'token must contain usable characters';
  }
  if (repeatedPatternLength(normalized) !== null) {
    return 'token must not repeat a short pattern';
  }
  if (/(.)\1{7,}/.test(normalized)) {
    return 'token must not contain long repeated character runs';
  }
  if (new Set(normalized).size < 8) {
    return 'token must contain more variation';
  }
  return null;
}

function validateSourceIdentifier(value: string, label: string): string | null {
  const trimmed = value.trim();
  if (!/^[a-zA-Z0-9._:-]{1,128}$/.test(trimmed)) {
    return `${label} contains unsupported characters`;
  }
  return null;
}

async function importPbkdf2Key(secret: string): Promise<CryptoKey> {
  return crypto.subtle.importKey(
    'raw',
    encoder.encode(secret),
    'PBKDF2',
    false,
    ['deriveBits']
  );
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

async function deriveVerifier(token: string, salt: string, pepper: string, iterations: number): Promise<string> {
  const key = await importPbkdf2Key(`${token}\u0000${pepper}`);
  const saltBytes = base64UrlToBytes(salt);
  const bits = await crypto.subtle.deriveBits({
    name: 'PBKDF2',
    hash: 'SHA-256',
    salt: saltBytes.buffer.slice(saltBytes.byteOffset, saltBytes.byteOffset + saltBytes.byteLength) as ArrayBuffer,
    iterations
  }, key, 256);
  return bytesToBase64Url(new Uint8Array(bits));
}

async function sign(secret: string, payload: string): Promise<string> {
  const key = await importHmacKey(secret);
  const signature = await crypto.subtle.sign('HMAC', key, encoder.encode(payload));
  return bytesToBase64Url(new Uint8Array(signature));
}

function nodeInfo(record: NodeTokenRecord): NodeTokenInfo {
  return {
    key_id: record.keyId,
    source_service: record.sourceService,
    source_instance: record.sourceInstance,
    masked_prefix: record.maskedPrefix,
    created_at: record.createdAt,
    last_used_at: record.lastUsedAt
  };
}

export class TokenStore {
  constructor(
    private readonly storage: DurableObjectStorage,
    private readonly bootstrapToken: string,
    private readonly pepper: string,
    private readonly now: () => number = () => Date.now()
  ) {}

  async validateOperator(candidate: string): Promise<boolean> {
    const record = await this.ensureOperatorRecord();
    const derived = await deriveVerifier(candidate.trim(), record.salt, this.pepper, record.iterations);
    return constantTimeEqual(base64UrlToBytes(derived), base64UrlToBytes(record.verifier));
  }

  async info(): Promise<OperatorTokenInfo> {
    const record = await this.ensureOperatorRecord();
    return {
      masked_prefix: record.maskedPrefix,
      created_at: record.createdAt
    };
  }

  async sessionSecret(): Promise<string> {
    const record = await this.ensureOperatorRecord();
    return record.sessionSecret;
  }

  async rotate(body: RotateBody): Promise<{ info: OperatorTokenInfo; token?: string }> {
    const nextToken = body.generate ? randomToken() : body.token?.trim() ?? '';
    const error = validateSharedTokenFormat(nextToken);
    if (error) {
      throw new Error(error);
    }

    const current = await this.ensureOperatorRecord();
    const salt = randomToken(16);
    const record: OperatorRecord = {
      version: PBKDF2_VERSION,
      iterations: PBKDF2_ITERATIONS,
      salt,
      verifier: await deriveVerifier(nextToken, salt, this.pepper, PBKDF2_ITERATIONS),
      maskedPrefix: maskToken(nextToken),
      createdAt: this.now(),
      sessionSecret: current.sessionSecret
    };
    await this.storage.put(OPERATOR_RECORD_KEY, record);
    return {
      info: {
        masked_prefix: record.maskedPrefix,
        created_at: record.createdAt
      },
      token: body.generate ? nextToken : undefined
    };
  }

  async listNodeTokens(): Promise<NodeTokenInfo[]> {
    const records = await this.nodeTokenRecords();
    return Object.values(records)
      .sort((left, right) => {
        const service = left.sourceService.localeCompare(right.sourceService);
        if (service !== 0) {
          return service;
        }
        return left.sourceInstance.localeCompare(right.sourceInstance);
      })
      .map(nodeInfo);
  }

  async createNodeToken(sourceServiceInput: string, sourceInstanceInput: string): Promise<CreateNodeTokenResponse> {
    const sourceService = sourceServiceInput.trim();
    const sourceInstance = sourceInstanceInput.trim();
    const serviceError = validateSourceIdentifier(sourceService, 'source_service');
    if (serviceError) {
      throw new Error(serviceError);
    }
    const instanceError = validateSourceIdentifier(sourceInstance, 'source_instance');
    if (instanceError) {
      throw new Error(instanceError);
    }

    const lookupKey = `${sourceService}\u0000${sourceInstance}`;
    const sourceLookup = await this.sourceLookup();
    if (sourceLookup[lookupKey]) {
      throw new Error('source tuple already exists');
    }

    const secret = randomToken();
    const record: NodeTokenRecord = {
      keyId: randomToken(12),
      sourceService,
      sourceInstance,
      secret,
      maskedPrefix: maskToken(secret),
      createdAt: this.now(),
      lastUsedAt: null
    };
    const records = await this.nodeTokenRecords();
    records[record.keyId] = record;
    sourceLookup[lookupKey] = record.keyId;
    await this.storage.put(NODE_TOKENS_KEY, records);
    await this.storage.put(SOURCE_LOOKUP_KEY, sourceLookup);
    return {
      key_id: record.keyId,
      token: secret,
      info: nodeInfo(record)
    };
  }

  async revokeNodeToken(keyIdInput: string): Promise<void> {
    const keyId = keyIdInput.trim();
    const records = await this.nodeTokenRecords();
    const record = records[keyId];
    if (!record) {
      throw new Error('node token not found');
    }
    delete records[keyId];
    const sourceLookup = await this.sourceLookup();
    delete sourceLookup[`${record.sourceService}\u0000${record.sourceInstance}`];
    await this.storage.put(NODE_TOKENS_KEY, records);
    await this.storage.put(SOURCE_LOOKUP_KEY, sourceLookup);
  }

  async validateIngress(body: ValidateIngressBody): Promise<{ valid: boolean; error?: string }> {
    const keyId = body.key_id?.trim() ?? '';
    const timestamp = body.timestamp?.trim() ?? '';
    const signature = body.signature?.trim() ?? '';
    const rawBody = body.raw_body ?? '';
    const sourceService = body.source_service?.trim() ?? '';
    const sourceInstance = body.source_instance?.trim() ?? '';

    if (!keyId || !timestamp || !signature || !rawBody || !sourceService || !sourceInstance) {
      return { valid: false, error: 'missing ingress authentication fields' };
    }
    const headerTimestamp = Number(timestamp);
    if (!Number.isInteger(headerTimestamp)) {
      return { valid: false, error: 'timestamp must be epoch seconds' };
    }
    const skewSeconds = Math.abs(Math.floor(this.now() / 1000) - headerTimestamp);
    if (skewSeconds > INGEST_MAX_SKEW_SECONDS) {
      return { valid: false, error: 'stale timestamp' };
    }

    const records = await this.nodeTokenRecords();
    const record = records[keyId];
    if (!record) {
      return { valid: false, error: 'unknown key id' };
    }
    if (record.sourceService !== sourceService || record.sourceInstance !== sourceInstance) {
      return { valid: false, error: 'source mismatch' };
    }

    const expected = await sign(record.secret, `${timestamp}.${rawBody}`);
    try {
      if (!constantTimeEqual(base64UrlToBytes(expected), base64UrlToBytes(signature))) {
        return { valid: false, error: 'invalid signature' };
      }
    } catch {
      return { valid: false, error: 'invalid signature' };
    }

    records[keyId] = {
      ...record,
      lastUsedAt: this.now()
    };
    await this.storage.put(NODE_TOKENS_KEY, records);
    return { valid: true };
  }

  private async ensureOperatorRecord(): Promise<OperatorRecord> {
    const existing = await this.storage.get<OperatorRecord>(OPERATOR_RECORD_KEY);
    if (existing) {
      return existing;
    }

    const bootstrap = this.bootstrapToken.trim();
    const error = validateSharedTokenFormat(bootstrap);
    if (error) {
      throw new Error(`invalid bootstrap operator token: ${error}`);
    }

    const salt = randomToken(16);
    const record: OperatorRecord = {
      version: PBKDF2_VERSION,
      iterations: PBKDF2_ITERATIONS,
      salt,
      verifier: await deriveVerifier(bootstrap, salt, this.pepper, PBKDF2_ITERATIONS),
      maskedPrefix: maskToken(bootstrap),
      createdAt: this.now(),
      sessionSecret: randomToken()
    };
    await this.storage.put(OPERATOR_RECORD_KEY, record);
    return record;
  }

  private async nodeTokenRecords(): Promise<NodeTokenRecords> {
    return (await this.storage.get<NodeTokenRecords>(NODE_TOKENS_KEY)) ?? {};
  }

  private async sourceLookup(): Promise<SourceLookup> {
    return (await this.storage.get<SourceLookup>(SOURCE_LOOKUP_KEY)) ?? {};
  }
}

export class TokenDurableObject {
  private readonly store: TokenStore;

  constructor(state: DurableObjectState, env: { FBNOTIFY_OPERATOR_TOKEN: string; FBNOTIFY_TOKEN_PEPPER: string }) {
    this.store = new TokenStore(state.storage, env.FBNOTIFY_OPERATOR_TOKEN, env.FBNOTIFY_TOKEN_PEPPER);
  }

  async fetch(request: Request): Promise<Response> {
    const url = new URL(request.url);

    if (request.method === 'POST' && url.pathname === '/validate-operator') {
      const body = await request.json() as { token?: string };
      return json({ valid: await this.store.validateOperator(body.token?.trim() ?? '') });
    }

    if (request.method === 'GET' && url.pathname === '/session-secret') {
      return json({ session_secret: await this.store.sessionSecret() });
    }

    if (request.method === 'GET' && url.pathname === '/info') {
      return json(await this.store.info());
    }

    if (request.method === 'POST' && url.pathname === '/rotate') {
      const body = await request.json() as RotateBody;
      try {
        return json(await this.store.rotate(body));
      } catch (error) {
        return json({ error: error instanceof Error ? error.message : 'invalid token' }, 400);
      }
    }

    if (request.method === 'GET' && url.pathname === '/node-tokens') {
      return json({ tokens: await this.store.listNodeTokens() });
    }

    if (request.method === 'POST' && url.pathname === '/node-tokens') {
      const body = await request.json() as CreateNodeTokenBody;
      try {
        return json(await this.store.createNodeToken(body.source_service ?? '', body.source_instance ?? ''));
      } catch (error) {
        return json({ error: error instanceof Error ? error.message : 'invalid node token request' }, 400);
      }
    }

    if (request.method === 'DELETE' && url.pathname.startsWith('/node-tokens/')) {
      const keyId = decodeURIComponent(url.pathname.slice('/node-tokens/'.length));
      try {
        await this.store.revokeNodeToken(keyId);
        return json({ ok: true });
      } catch (error) {
        return json({ error: error instanceof Error ? error.message : 'node token not found' }, 404);
      }
    }

    if (request.method === 'POST' && url.pathname === '/validate-ingress') {
      const body = await request.json() as ValidateIngressBody;
      const result = await this.store.validateIngress(body);
      return json(result, result.valid ? 200 : 401);
    }

    return json({ error: 'not found' }, 404);
  }
}
