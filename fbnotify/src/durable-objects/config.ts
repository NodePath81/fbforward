import { validateTargetIdentifier } from '../schema';
import type {
  NotificationEvent,
  ProviderTargetConfig,
  ProviderTargetRecord,
  ProviderTargetSummary,
  RouteRecord,
  RouteSummary
} from '../types';

const TARGETS_KEY = 'targets';
const ROUTES_KEY = 'routes';

interface CreateTargetBody {
  name?: string;
  type?: string;
  config?: Record<string, unknown>;
}

interface UpdateTargetBody {
  name?: string;
  config?: Record<string, unknown>;
}

interface CreateRouteBody {
  name?: string;
  source_service?: string | null;
  event_name?: string | null;
  target_ids?: unknown;
}

interface UpdateRouteBody extends CreateRouteBody {}

function json(data: unknown, status: number = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: {
      'content-type': 'application/json; charset=utf-8'
    }
  });
}

function randomId(prefix: string): string {
  const bytes = new Uint8Array(12);
  crypto.getRandomValues(bytes);
  const token = btoa(String.fromCharCode(...bytes)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '');
  return `${prefix}_${token}`;
}

function normalizeName(value: unknown, label: string): string {
  if (typeof value !== 'string') {
    throw new Error(`${label} must be a string`);
  }
  const trimmed = value.trim();
  if (trimmed === '') {
    throw new Error(`${label} is required`);
  }
  if (trimmed.length > 128) {
    throw new Error(`${label} must be at most 128 characters`);
  }
  return trimmed;
}

function normalizeOptionalIdentifier(value: unknown, label: string): string | null {
  if (value === null || value === undefined || value === '') {
    return null;
  }
  const error = validateTargetIdentifier(value, label);
  if (error) {
    throw new Error(error);
  }
  return String(value).trim();
}

function normalizeWebhookUrl(value: unknown): string {
  if (typeof value !== 'string') {
    throw new Error('config.url must be a string');
  }
  const trimmed = value.trim();
  let parsed: URL;
  try {
    parsed = new URL(trimmed);
  } catch {
    throw new Error('config.url must be a valid URL');
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    throw new Error('config.url must use http or https');
  }
  return parsed.toString();
}

function maskValue(value: string): string {
  if (value.length <= 8) {
    return `${value}...`;
  }
  return `${value.slice(0, 8)}...`;
}

function summarizeTarget(target: ProviderTargetRecord): ProviderTargetSummary {
  const summary: Record<string, string | number | boolean | null> = {};
  if (target.type === 'webhook') {
    const parsed = new URL(target.config.type === 'webhook' ? target.config.url : 'https://invalid.local/');
    summary.url_host = parsed.host;
    summary.url_path = parsed.pathname;
  } else if (target.type === 'pushover') {
    summary.user_key = maskValue(target.config.type === 'pushover' ? target.config.user_key : '');
    summary.api_token = maskValue(target.config.type === 'pushover' ? target.config.api_token : '');
    summary.device = target.config.type === 'pushover' ? (target.config.device ?? null) : null;
  }
  return {
    id: target.id,
    name: target.name,
    type: target.type,
    created_at: target.created_at,
    updated_at: target.updated_at,
    summary
  };
}

function routeMatchKind(route: RouteRecord): RouteSummary['match_kind'] {
  if (route.source_service && route.event_name) {
    return 'service_event';
  }
  if (route.event_name) {
    return 'event';
  }
  if (route.source_service) {
    return 'service_default';
  }
  return 'global_default';
}

export function summarizeRoute(route: RouteRecord): RouteSummary {
  return {
    ...route,
    match_kind: routeMatchKind(route)
  };
}

function routeKey(sourceService: string | null, eventName: string | null): string {
  return `${sourceService ?? ''}\u0000${eventName ?? ''}`;
}

export function resolveRouteTargets(
  event: NotificationEvent,
  routes: RouteRecord[],
  targets: ProviderTargetRecord[]
): ProviderTargetRecord[] {
  const routeMap = new Map(routes.map(route => [routeKey(route.source_service, route.event_name), route]));
  const match = routeMap.get(routeKey(event.source.service, event.event_name))
    ?? routeMap.get(routeKey(null, event.event_name))
    ?? routeMap.get(routeKey(event.source.service, null))
    ?? routeMap.get(routeKey(null, null));

  if (!match) {
    return [];
  }
  const targetsById = new Map(targets.map(target => [target.id, target]));
  return match.target_ids
    .map(targetId => targetsById.get(targetId))
    .filter((target): target is ProviderTargetRecord => Boolean(target));
}

export class ConfigStore {
  constructor(
    private readonly storage: DurableObjectStorage,
    private readonly now: () => number = () => Date.now()
  ) {}

  async listTargets(): Promise<ProviderTargetSummary[]> {
    return (await this.targets()).map(summarizeTarget);
  }

  async createTarget(body: CreateTargetBody): Promise<ProviderTargetSummary> {
    const name = normalizeName(body.name, 'name');
    const type = body.type?.trim();
    if (type !== 'webhook' && type !== 'pushover' && type !== 'capture') {
      throw new Error('type must be one of webhook, pushover, capture');
    }
    const config = this.normalizeTargetConfig(type, body.config ?? {}, null);
    const now = this.now();
    const record: ProviderTargetRecord = {
      id: randomId('tgt'),
      name,
      type,
      config,
      created_at: now,
      updated_at: now
    };
    const targets = await this.targets();
    targets.push(record);
    await this.storage.put(TARGETS_KEY, targets);
    return summarizeTarget(record);
  }

  async updateTarget(idInput: string, body: UpdateTargetBody): Promise<ProviderTargetSummary> {
    const id = idInput.trim();
    const targets = await this.targets();
    const index = targets.findIndex(target => target.id === id);
    if (index < 0) {
      throw new Error('target not found');
    }
    const existing = targets[index]!;
    const next: ProviderTargetRecord = {
      ...existing,
      name: body.name === undefined ? existing.name : normalizeName(body.name, 'name'),
      config: this.normalizeTargetConfig(existing.type, body.config ?? {}, existing.config),
      updated_at: this.now()
    };
    targets[index] = next;
    await this.storage.put(TARGETS_KEY, targets);
    return summarizeTarget(next);
  }

  async deleteTarget(idInput: string): Promise<void> {
    const id = idInput.trim();
    const routes = await this.routes();
    if (routes.some(route => route.target_ids.includes(id))) {
      throw new Error('target is still referenced by a route');
    }
    const targets = await this.targets();
    const next = targets.filter(target => target.id !== id);
    if (next.length === targets.length) {
      throw new Error('target not found');
    }
    await this.storage.put(TARGETS_KEY, next);
  }

  async listRoutes(): Promise<RouteSummary[]> {
    return (await this.routes()).map(summarizeRoute);
  }

  async createRoute(body: CreateRouteBody): Promise<RouteSummary> {
    const route = await this.normalizeRoute(body, null);
    const routes = await this.routes();
    this.assertUniqueRoute(routes, route, null);
    routes.push(route);
    await this.storage.put(ROUTES_KEY, routes);
    return summarizeRoute(route);
  }

  async updateRoute(idInput: string, body: UpdateRouteBody): Promise<RouteSummary> {
    const id = idInput.trim();
    const routes = await this.routes();
    const index = routes.findIndex(route => route.id === id);
    if (index < 0) {
      throw new Error('route not found');
    }
    const route = await this.normalizeRoute(body, routes[index]!);
    this.assertUniqueRoute(routes, route, id);
    routes[index] = route;
    await this.storage.put(ROUTES_KEY, routes);
    return summarizeRoute(route);
  }

  async deleteRoute(idInput: string): Promise<void> {
    const id = idInput.trim();
    const routes = await this.routes();
    const next = routes.filter(route => route.id !== id);
    if (next.length === routes.length) {
      throw new Error('route not found');
    }
    await this.storage.put(ROUTES_KEY, next);
  }

  async resolveTargetsForEvent(event: NotificationEvent): Promise<ProviderTargetRecord[]> {
    return resolveRouteTargets(event, await this.routes(), await this.targets());
  }

  async targetsByIds(targetIds: string[]): Promise<ProviderTargetRecord[]> {
    const targets = await this.targets();
    const byId = new Map(targets.map(target => [target.id, target]));
    const resolved = targetIds.map(id => byId.get(id)).filter((target): target is ProviderTargetRecord => Boolean(target));
    if (resolved.length !== targetIds.length) {
      throw new Error('unknown target id in selection');
    }
    return resolved;
  }

  private async normalizeRoute(body: CreateRouteBody, existing: RouteRecord | null): Promise<RouteRecord> {
    const name = body.name === undefined ? existing?.name : normalizeName(body.name, 'name');
    if (!name) {
      throw new Error('name is required');
    }
    const sourceService = body.source_service === undefined
      ? (existing?.source_service ?? null)
      : normalizeOptionalIdentifier(body.source_service, 'source_service');
    const eventName = body.event_name === undefined
      ? (existing?.event_name ?? null)
      : normalizeOptionalIdentifier(body.event_name, 'event_name');
    if (!sourceService && !eventName && !existing && body.target_ids === undefined) {
      throw new Error('target_ids is required');
    }

    const targetIds = body.target_ids === undefined
      ? [...(existing?.target_ids ?? [])]
      : this.normalizeTargetIds(body.target_ids);

    const knownTargetIds = new Set((await this.targets()).map(target => target.id));
    if (targetIds.some(id => !knownTargetIds.has(id))) {
      throw new Error('route contains unknown target ids');
    }

    const now = this.now();
    return {
      id: existing?.id ?? randomId('route'),
      name,
      source_service: sourceService,
      event_name: eventName,
      target_ids: targetIds,
      created_at: existing?.created_at ?? now,
      updated_at: now
    };
  }

  private normalizeTargetIds(value: unknown): string[] {
    if (!Array.isArray(value) || value.length === 0) {
      throw new Error('target_ids must be a non-empty array');
    }
    const normalized = value.map((item, index) => {
      const error = validateTargetIdentifier(item, `target_ids[${index}]`);
      if (error) {
        throw new Error(error);
      }
      return String(item).trim();
    });
    return Array.from(new Set(normalized));
  }

  private assertUniqueRoute(routes: RouteRecord[], candidate: RouteRecord, currentId: string | null): void {
    const key = routeKey(candidate.source_service, candidate.event_name);
    if (routes.some(route => route.id !== currentId && routeKey(route.source_service, route.event_name) === key)) {
      throw new Error('a route with the same match scope already exists');
    }
  }

  private normalizeTargetConfig(
    type: ProviderTargetRecord['type'],
    config: Record<string, unknown>,
    existing: ProviderTargetConfig | null
  ): ProviderTargetConfig {
    if (type === 'webhook') {
      const url = config.url === undefined
        ? (existing && existing.type === 'webhook' ? existing.url : undefined)
        : normalizeWebhookUrl(config.url);
      if (!url) {
        throw new Error('config.url is required');
      }
      return { type, url };
    }
    if (type === 'pushover') {
      const apiToken = config.api_token === undefined
        ? (existing && existing.type === 'pushover' ? existing.api_token : undefined)
        : normalizeName(config.api_token, 'config.api_token');
      const userKey = config.user_key === undefined
        ? (existing && existing.type === 'pushover' ? existing.user_key : undefined)
        : normalizeName(config.user_key, 'config.user_key');
      const device = config.device === undefined
        ? (existing && existing.type === 'pushover' ? existing.device : undefined)
        : (config.device === null || config.device === '' ? undefined : normalizeName(config.device, 'config.device'));
      if (!apiToken || !userKey) {
        throw new Error('config.api_token and config.user_key are required');
      }
      return {
        type,
        api_token: apiToken,
        user_key: userKey,
        ...(device ? { device } : {})
      };
    }
    return { type };
  }

  private async targets(): Promise<ProviderTargetRecord[]> {
    return (await this.storage.get<ProviderTargetRecord[]>(TARGETS_KEY)) ?? [];
  }

  private async routes(): Promise<RouteRecord[]> {
    return (await this.storage.get<RouteRecord[]>(ROUTES_KEY)) ?? [];
  }
}

export class ConfigDurableObject {
  private readonly store: ConfigStore;

  constructor(state: DurableObjectState) {
    this.store = new ConfigStore(state.storage);
  }

  async fetch(request: Request): Promise<Response> {
    const url = new URL(request.url);

    if (request.method === 'GET' && url.pathname === '/targets') {
      return json({ targets: await this.store.listTargets() });
    }

    if (request.method === 'POST' && url.pathname === '/targets') {
      try {
        return json(await this.store.createTarget(await request.json() as CreateTargetBody));
      } catch (error) {
        return json({ error: error instanceof Error ? error.message : 'invalid target request' }, 400);
      }
    }

    if (request.method === 'PUT' && url.pathname.startsWith('/targets/')) {
      try {
        return json(await this.store.updateTarget(
          decodeURIComponent(url.pathname.slice('/targets/'.length)),
          await request.json() as UpdateTargetBody
        ));
      } catch (error) {
        return json({ error: error instanceof Error ? error.message : 'invalid target request' }, 400);
      }
    }

    if (request.method === 'DELETE' && url.pathname.startsWith('/targets/')) {
      try {
        await this.store.deleteTarget(decodeURIComponent(url.pathname.slice('/targets/'.length)));
        return json({ ok: true });
      } catch (error) {
        return json({ error: error instanceof Error ? error.message : 'invalid target delete' }, 400);
      }
    }

    if (request.method === 'GET' && url.pathname === '/routes') {
      return json({ routes: await this.store.listRoutes() });
    }

    if (request.method === 'POST' && url.pathname === '/routes') {
      try {
        return json(await this.store.createRoute(await request.json() as CreateRouteBody));
      } catch (error) {
        return json({ error: error instanceof Error ? error.message : 'invalid route request' }, 400);
      }
    }

    if (request.method === 'PUT' && url.pathname.startsWith('/routes/')) {
      try {
        return json(await this.store.updateRoute(
          decodeURIComponent(url.pathname.slice('/routes/'.length)),
          await request.json() as UpdateRouteBody
        ));
      } catch (error) {
        return json({ error: error instanceof Error ? error.message : 'invalid route request' }, 400);
      }
    }

    if (request.method === 'DELETE' && url.pathname.startsWith('/routes/')) {
      try {
        await this.store.deleteRoute(decodeURIComponent(url.pathname.slice('/routes/'.length)));
        return json({ ok: true });
      } catch (error) {
        return json({ error: error instanceof Error ? error.message : 'invalid route delete' }, 400);
      }
    }

    if (request.method === 'POST' && url.pathname === '/resolve') {
      try {
        const body = await request.json() as { event?: NotificationEvent };
        return json({ targets: await this.store.resolveTargetsForEvent(body.event as NotificationEvent) });
      } catch (error) {
        return json({ error: error instanceof Error ? error.message : 'unable to resolve targets' }, 400);
      }
    }

    if (request.method === 'POST' && url.pathname === '/targets/by-ids') {
      try {
        const body = await request.json() as { target_ids?: string[] };
        return json({ targets: await this.store.targetsByIds(body.target_ids ?? []) });
      } catch (error) {
        return json({ error: error instanceof Error ? error.message : 'unable to resolve targets' }, 400);
      }
    }

    return json({ error: 'not found' }, 404);
  }
}
