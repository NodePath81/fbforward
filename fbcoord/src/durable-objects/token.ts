import { validateNodeId } from '../validation';

const TOKEN_RECORD_KEY = 'token_record';
const NODE_TOKEN_RECORDS_KEY = 'node_token_records';
const NODE_TOKEN_LOOKUP_KEY = 'node_token_lookup';
const MASK_PREFIX_LENGTH = 8;
const MIN_TOKEN_LENGTH = 32;
const PBKDF2_ITERATIONS = 50_000;
const PBKDF2_VERSION = 'pbkdf2-sha256-v1';

export interface TokenInfo {
  masked_prefix: string;
  created_at: number;
}

export interface NodeTokenInfo {
  node_id: string;
  masked_prefix: string;
  created_at: number;
  last_used_at: number | null;
}

interface ValidateBody {
  token?: string;
}

interface RotateBody {
  token?: string;
  generate?: boolean;
}

interface CreateNodeTokenBody {
  node_id?: string;
}

interface LegacyTokenRecord {
  tokenHash: string;
  maskedPrefix: string;
  createdAt: number;
  sessionSecret: string;
}

export interface TokenRecord {
  version: typeof PBKDF2_VERSION;
  iterations: number;
  salt: string;
  verifier: string;
  maskedPrefix: string;
  createdAt: number;
  sessionSecret: string;
}

interface StoredNodeTokenRecord {
  version: typeof PBKDF2_VERSION;
  iterations: number;
  salt: string;
  verifier: string;
  lookupHash: string;
  maskedPrefix: string;
  createdAt: number;
  lastUsedAt: number | null;
}

type NodeTokenRecords = Record<string, StoredNodeTokenRecord>;
type NodeTokenLookup = Record<string, string>;

const encoder = new TextEncoder();

class ConflictError extends Error {}
class NotFoundError extends Error {}

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

async function sha256(value: string): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', encoder.encode(value));
  return bytesToBase64Url(new Uint8Array(digest));
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

async function deriveVerifier(token: string, salt: string, pepper: string, iterations: number): Promise<string> {
  const key = await importPbkdf2Key(`${token}\u0000${pepper}`);
  const saltBytes = base64UrlToBytes(salt);
  const saltBuffer = saltBytes.buffer.slice(
    saltBytes.byteOffset,
    saltBytes.byteOffset + saltBytes.byteLength
  ) as ArrayBuffer;
  const bits = await crypto.subtle.deriveBits({
    name: 'PBKDF2',
    hash: 'SHA-256',
    salt: saltBuffer,
    iterations
  }, key, 256);
  return bytesToBase64Url(new Uint8Array(bits));
}

async function deriveNodeLookupHash(token: string, pepper: string): Promise<string> {
  return sha256(`${token}\u0000${pepper}\u0000node`);
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

  const weakRepeatedPattern = repeatedPatternLength(normalized);
  if (weakRepeatedPattern !== null) {
    return 'token must not repeat a short pattern';
  }

  if (/(.)\1{7,}/.test(normalized)) {
    return 'token must not contain long repeated character runs';
  }

  const uniqueCharacters = new Set(normalized);
  if (uniqueCharacters.size < 8) {
    return 'token must contain more variation';
  }

  return null;
}

function isCurrentRecord(record: LegacyTokenRecord | TokenRecord): record is TokenRecord {
  return (record as TokenRecord).version === PBKDF2_VERSION;
}

function json(data: unknown, status: number = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: {
      'content-type': 'application/json; charset=utf-8'
    }
  });
}

function nodeTokenInfo(nodeId: string, record: StoredNodeTokenRecord): NodeTokenInfo {
  return {
    node_id: nodeId,
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
    const record = await this.ensureRecord();
    const trimmed = candidate.trim();
    if (isCurrentRecord(record)) {
      const derived = await deriveVerifier(trimmed, record.salt, this.pepper, record.iterations);
      return constantTimeEqual(base64UrlToBytes(derived), base64UrlToBytes(record.verifier));
    }

    const candidateHash = await sha256(trimmed);
    if (candidateHash !== record.tokenHash) {
      return false;
    }

    const migrated = await this.createRecord(trimmed, record.maskedPrefix, record.createdAt, record.sessionSecret);
    await this.storage.put(TOKEN_RECORD_KEY, migrated);
    return true;
  }

  async validate(candidate: string): Promise<boolean> {
    return this.validateOperator(candidate);
  }

  async validateNode(candidate: string): Promise<{ valid: boolean; node_id?: string }> {
    const token = candidate.trim();
    if (token === '') {
      return { valid: false };
    }

    const lookupHash = await deriveNodeLookupHash(token, this.pepper);
    const lookup = await this.nodeTokenLookup();
    const nodeId = lookup[lookupHash];
    if (!nodeId) {
      return { valid: false };
    }

    const records = await this.nodeTokenRecords();
    const record = records[nodeId];
    if (!record) {
      delete lookup[lookupHash];
      await this.storage.put(NODE_TOKEN_LOOKUP_KEY, lookup);
      return { valid: false };
    }

    const derived = await deriveVerifier(token, record.salt, this.pepper, record.iterations);
    if (!constantTimeEqual(base64UrlToBytes(derived), base64UrlToBytes(record.verifier))) {
      return { valid: false };
    }

    records[nodeId] = {
      ...record,
      lastUsedAt: this.now()
    };
    await this.storage.put(NODE_TOKEN_RECORDS_KEY, records);

    return { valid: true, node_id: nodeId };
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
    const record = await this.createRecord(nextToken, maskToken(nextToken), this.now(), current.sessionSecret);
    await this.storage.put(TOKEN_RECORD_KEY, record);

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
    return Object.entries(records)
      .map(([nodeId, record]) => nodeTokenInfo(nodeId, record))
      .sort((left, right) => left.node_id.localeCompare(right.node_id));
  }

  async createNodeToken(nodeIdInput: string): Promise<{ token: string; info: NodeTokenInfo }> {
    const nodeId = nodeIdInput.trim();
    const nodeIdError = validateNodeId(nodeId);
    if (nodeIdError) {
      throw new Error(nodeIdError);
    }

    const records = await this.nodeTokenRecords();
    if (records[nodeId]) {
      throw new ConflictError('node_id already exists');
    }

    const token = randomToken();
    const createdAt = this.now();
    const salt = randomToken(16);
    const lookupHash = await deriveNodeLookupHash(token, this.pepper);
    const record: StoredNodeTokenRecord = {
      version: PBKDF2_VERSION,
      iterations: PBKDF2_ITERATIONS,
      salt,
      verifier: await deriveVerifier(token, salt, this.pepper, PBKDF2_ITERATIONS),
      lookupHash,
      maskedPrefix: maskToken(token),
      createdAt,
      lastUsedAt: null
    };

    const lookup = await this.nodeTokenLookup();
    records[nodeId] = record;
    lookup[lookupHash] = nodeId;
    await this.storage.put(NODE_TOKEN_RECORDS_KEY, records);
    await this.storage.put(NODE_TOKEN_LOOKUP_KEY, lookup);

    return {
      token,
      info: nodeTokenInfo(nodeId, record)
    };
  }

  async revokeNodeToken(nodeIdInput: string): Promise<void> {
    const nodeId = nodeIdInput.trim();
    const records = await this.nodeTokenRecords();
    const record = records[nodeId];
    if (!record) {
      throw new NotFoundError('node token not found');
    }

    const lookup = await this.nodeTokenLookup();
    delete records[nodeId];
    delete lookup[record.lookupHash];
    await this.storage.put(NODE_TOKEN_RECORDS_KEY, records);
    await this.storage.put(NODE_TOKEN_LOOKUP_KEY, lookup);
  }

  private async ensureRecord(): Promise<LegacyTokenRecord | TokenRecord> {
    const existing = await this.storage.get<LegacyTokenRecord | TokenRecord>(TOKEN_RECORD_KEY);
    if (existing) {
      return existing;
    }

    const bootstrap = this.bootstrapToken.trim();
    const error = validateSharedTokenFormat(bootstrap);
    if (error) {
      throw new Error(`FBCOORD_TOKEN ${error}`);
    }

    const record = await this.createRecord(bootstrap, maskToken(bootstrap), this.now(), randomToken());
    await this.storage.put(TOKEN_RECORD_KEY, record);
    return record;
  }

  private async createRecord(
    token: string,
    maskedPrefix: string,
    createdAt: number,
    sessionSecret: string
  ): Promise<TokenRecord> {
    const salt = randomToken(16);
    return {
      version: PBKDF2_VERSION,
      iterations: PBKDF2_ITERATIONS,
      salt,
      verifier: await deriveVerifier(token, salt, this.pepper, PBKDF2_ITERATIONS),
      maskedPrefix,
      createdAt,
      sessionSecret
    };
  }

  private async nodeTokenRecords(): Promise<NodeTokenRecords> {
    return await this.storage.get<NodeTokenRecords>(NODE_TOKEN_RECORDS_KEY) ?? {};
  }

  private async nodeTokenLookup(): Promise<NodeTokenLookup> {
    return await this.storage.get<NodeTokenLookup>(NODE_TOKEN_LOOKUP_KEY) ?? {};
  }
}

export class TokenDurableObject {
  private readonly store: TokenStore;

  constructor(state: DurableObjectState, env: { FBCOORD_TOKEN: string; FBCOORD_TOKEN_PEPPER: string }) {
    this.store = new TokenStore(state.storage, env.FBCOORD_TOKEN, env.FBCOORD_TOKEN_PEPPER);
  }

  async fetch(request: Request): Promise<Response> {
    const url = new URL(request.url);

    if (request.method === 'POST' && url.pathname === '/validate-operator') {
      const body = await request.json() as ValidateBody;
      const token = body.token?.trim();
      if (!token) {
        return json({ valid: false }, 400);
      }
      return json({ valid: await this.store.validateOperator(token) });
    }

    if (request.method === 'POST' && url.pathname === '/validate-node') {
      const body = await request.json() as ValidateBody;
      const token = body.token?.trim();
      if (!token) {
        return json({ valid: false }, 400);
      }
      return json(await this.store.validateNode(token));
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

    if (request.method === 'GET' && url.pathname === '/node-tokens') {
      return json({ tokens: await this.store.listNodeTokens() });
    }

    if (request.method === 'POST' && url.pathname === '/node-tokens') {
      try {
        const body = await request.json() as CreateNodeTokenBody;
        return json(await this.store.createNodeToken(body.node_id ?? ''));
      } catch (error) {
        if (error instanceof ConflictError) {
          return json({ error: error.message }, 409);
        }
        return json({ error: error instanceof Error ? error.message : 'invalid node token request' }, 400);
      }
    }

    if (request.method === 'DELETE' && url.pathname.startsWith('/node-tokens/')) {
      try {
        const nodeId = decodeURIComponent(url.pathname.slice('/node-tokens/'.length));
        await this.store.revokeNodeToken(nodeId);
        return json({ ok: true });
      } catch (error) {
        if (error instanceof NotFoundError) {
          return json({ error: error.message }, 404);
        }
        return json({ error: error instanceof Error ? error.message : 'invalid node token request' }, 400);
      }
    }

    return new Response('not found', { status: 404 });
  }
}
