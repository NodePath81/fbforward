const ACTIVE_POOLS_KEY = 'active_pools';

interface PoolMutationBody {
  pool?: string;
}

export class RegistryStore {
  constructor(private readonly storage: DurableObjectStorage) {}

  async list(): Promise<string[]> {
    const pools = await this.storage.get<string[]>(ACTIVE_POOLS_KEY);
    return Array.isArray(pools) ? [...pools].sort((left, right) => left.localeCompare(right)) : [];
  }

  async register(pool: string): Promise<string[]> {
    const pools = new Set(await this.list());
    pools.add(pool);
    const next = Array.from(pools).sort((left, right) => left.localeCompare(right));
    await this.storage.put(ACTIVE_POOLS_KEY, next);
    return next;
  }

  async deregister(pool: string): Promise<string[]> {
    const pools = new Set(await this.list());
    pools.delete(pool);
    const next = Array.from(pools).sort((left, right) => left.localeCompare(right));
    await this.storage.put(ACTIVE_POOLS_KEY, next);
    return next;
  }
}

function json(data: unknown, status: number = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: {
      'content-type': 'application/json; charset=utf-8'
    }
  });
}

export class RegistryDurableObject {
  private readonly store: RegistryStore;

  constructor(state: DurableObjectState) {
    this.store = new RegistryStore(state.storage);
  }

  async fetch(request: Request): Promise<Response> {
    const url = new URL(request.url);

    if (request.method === 'GET' && url.pathname === '/list') {
      return json({ pools: await this.store.list() });
    }

    if (request.method === 'POST' && (url.pathname === '/register' || url.pathname === '/deregister')) {
      const body = await request.json() as PoolMutationBody;
      const pool = body.pool?.trim();
      if (!pool) {
        return json({ error: 'missing pool' }, 400);
      }

      const pools = url.pathname === '/register'
        ? await this.store.register(pool)
        : await this.store.deregister(pool);
      return json({ pools });
    }

    return new Response('not found', { status: 404 });
  }
}
