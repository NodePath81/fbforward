const AUTH_GUARD_STATE_KEY = 'state';
const MAX_FAILURES = 3;
const WINDOW_MS = 10 * 60_000;
const BLOCK_MS = 15 * 60_000;
const CLEANOUT_MS = 24 * 60 * 60_000;
const MAX_ESCALATION_LEVEL = 4;
const BAN_KV_PREFIX = 'ban';

export type AuthScope = 'login' | 'node-auth';

export interface GuardRecord {
  failures: number;
  firstFailureAt: number;
  blockedUntil: number;
  penaltyLevel: number;
  cleanUntil: number;
}

interface GuardRequestBody {
  scope?: AuthScope;
  client_key?: string;
}

export interface GuardStatusResponse {
  blocked: boolean;
  retry_after_seconds: number;
  penalty_level: number;
}

function json(data: unknown, status: number = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: {
      'content-type': 'application/json; charset=utf-8'
    }
  });
}

function parseGuardBody(body: GuardRequestBody): { scope: AuthScope; clientKey: string } | null {
  const scope = body.scope;
  const clientKey = body.client_key?.trim();
  if ((scope !== 'login' && scope !== 'node-auth') || !clientKey) {
    return null;
  }
  return { scope, clientKey };
}

export function activeBanKey(scope: AuthScope, clientKey: string): string {
  return `${BAN_KV_PREFIX}:${scope}:${clientKey}`;
}

export function manualDenyKey(scope: AuthScope | 'any', clientKey: string): string {
  return `deny:${scope}:${clientKey}`;
}

export class AuthGuardStore {
  constructor(
    private readonly storage: DurableObjectStorage,
    private readonly kv: KVNamespace,
    private readonly now: () => number = () => Date.now()
  ) {}

  async getStatus(scope: AuthScope, clientKey: string): Promise<GuardStatusResponse> {
    const now = this.now();
    const record = await this.normalizeRecord(await this.storage.get<GuardRecord>(AUTH_GUARD_STATE_KEY), scope, clientKey, now);
    if (!record) {
      return { blocked: false, retry_after_seconds: 0, penalty_level: 0 };
    }

    if (record.blockedUntil > now) {
      await this.writeBanMarker(scope, clientKey, record, now);
      return {
        blocked: true,
        retry_after_seconds: Math.max(1, Math.ceil((record.blockedUntil - now) / 1000)),
        penalty_level: record.penaltyLevel
      };
    }

    await this.clearBanMarker(scope, clientKey);
    return {
      blocked: false,
      retry_after_seconds: 0,
      penalty_level: record.penaltyLevel
    };
  }

  async recordFailure(scope: AuthScope, clientKey: string): Promise<GuardStatusResponse> {
    const now = this.now();
    const current = await this.normalizeRecord(await this.storage.get<GuardRecord>(AUTH_GUARD_STATE_KEY), scope, clientKey, now);
    const record = current ?? {
      failures: 0,
      firstFailureAt: 0,
      blockedUntil: 0,
      penaltyLevel: 0,
      cleanUntil: 0
    };

    if (record.blockedUntil > now) {
      await this.writeBanMarker(scope, clientKey, record, now);
      return {
        blocked: true,
        retry_after_seconds: Math.max(1, Math.ceil((record.blockedUntil - now) / 1000)),
        penalty_level: record.penaltyLevel
      };
    }

    if (record.failures === 0 || now - record.firstFailureAt > WINDOW_MS) {
      record.failures = 1;
      record.firstFailureAt = now;
    } else {
      record.failures += 1;
    }

    if (record.failures >= MAX_FAILURES) {
      record.penaltyLevel = now < record.cleanUntil
        ? Math.min(record.penaltyLevel + 1, MAX_ESCALATION_LEVEL)
        : 0;
      record.blockedUntil = now + (BLOCK_MS * (record.penaltyLevel + 1));
      record.cleanUntil = now + CLEANOUT_MS;
      record.failures = 0;
      record.firstFailureAt = 0;
      await this.storage.put(AUTH_GUARD_STATE_KEY, record);
      await this.writeBanMarker(scope, clientKey, record, now);
      return {
        blocked: true,
        retry_after_seconds: Math.max(1, Math.ceil((record.blockedUntil - now) / 1000)),
        penalty_level: record.penaltyLevel
      };
    }

    await this.storage.put(AUTH_GUARD_STATE_KEY, record);
    return {
      blocked: false,
      retry_after_seconds: 0,
      penalty_level: record.penaltyLevel
    };
  }

  async recordSuccess(scope: AuthScope, clientKey: string): Promise<GuardStatusResponse> {
    const now = this.now();
    const record = await this.normalizeRecord(await this.storage.get<GuardRecord>(AUTH_GUARD_STATE_KEY), scope, clientKey, now);
    if (!record) {
      return { blocked: false, retry_after_seconds: 0, penalty_level: 0 };
    }

    if (record.blockedUntil > now) {
      await this.writeBanMarker(scope, clientKey, record, now);
      return {
        blocked: true,
        retry_after_seconds: Math.max(1, Math.ceil((record.blockedUntil - now) / 1000)),
        penalty_level: record.penaltyLevel
      };
    }

    record.failures = Math.max(0, record.failures - 1);
    if (record.failures === 0) {
      record.firstFailureAt = 0;
    }

    if (record.failures === 0 && record.penaltyLevel === 0 && record.cleanUntil <= now) {
      await this.storage.delete(AUTH_GUARD_STATE_KEY);
    } else {
      await this.storage.put(AUTH_GUARD_STATE_KEY, record);
    }
    await this.clearBanMarker(scope, clientKey);

    return {
      blocked: false,
      retry_after_seconds: 0,
      penalty_level: record.penaltyLevel
    };
  }

  private async normalizeRecord(
    record: GuardRecord | undefined,
    scope: AuthScope,
    clientKey: string,
    now: number
  ): Promise<GuardRecord | null> {
    if (!record) {
      await this.clearBanMarker(scope, clientKey);
      return null;
    }

    const normalized = { ...record };
    if (normalized.failures > 0 && now - normalized.firstFailureAt > WINDOW_MS) {
      normalized.failures = 0;
      normalized.firstFailureAt = 0;
    }

    if (normalized.cleanUntil > 0 && now >= normalized.cleanUntil && normalized.blockedUntil <= now) {
      normalized.penaltyLevel = 0;
      normalized.cleanUntil = 0;
    }

    if (
      normalized.failures === 0 &&
      normalized.blockedUntil <= now &&
      normalized.penaltyLevel === 0 &&
      normalized.cleanUntil === 0
    ) {
      await this.storage.delete(AUTH_GUARD_STATE_KEY);
      await this.clearBanMarker(scope, clientKey);
      return null;
    }

    await this.storage.put(AUTH_GUARD_STATE_KEY, normalized);
    if (normalized.blockedUntil <= now) {
      await this.clearBanMarker(scope, clientKey);
    }
    return normalized;
  }

  private async writeBanMarker(
    scope: AuthScope,
    clientKey: string,
    record: GuardRecord,
    now: number
  ): Promise<void> {
    const ttlSeconds = Math.max(60, Math.ceil((record.blockedUntil - now) / 1000) + 60);
    await this.kv.put(
      activeBanKey(scope, clientKey),
      JSON.stringify({ blocked_until: record.blockedUntil }),
      { expirationTtl: ttlSeconds }
    );
  }

  private async clearBanMarker(scope: AuthScope, clientKey: string): Promise<void> {
    await this.kv.delete(activeBanKey(scope, clientKey));
  }
}

export class AuthGuardDurableObject {
  private readonly store: AuthGuardStore;

  constructor(state: DurableObjectState, env: { FBCOORD_AUTH_KV: KVNamespace }) {
    this.store = new AuthGuardStore(state.storage, env.FBCOORD_AUTH_KV);
  }

  async fetch(request: Request): Promise<Response> {
    const url = new URL(request.url);
    let parsedBody: GuardRequestBody;
    try {
      parsedBody = await request.json() as GuardRequestBody;
    } catch {
      return json({ error: 'invalid auth guard request' }, 400);
    }
    const body = parseGuardBody(parsedBody);
    if (!body) {
      return json({ error: 'invalid auth guard request' }, 400);
    }

    if (request.method === 'POST' && url.pathname === '/status') {
      return json(await this.store.getStatus(body.scope, body.clientKey));
    }

    if (request.method === 'POST' && url.pathname === '/failure') {
      return json(await this.store.recordFailure(body.scope, body.clientKey));
    }

    if (request.method === 'POST' && url.pathname === '/success') {
      return json(await this.store.recordSuccess(body.scope, body.clientKey));
    }

    return new Response('not found', { status: 404 });
  }
}
