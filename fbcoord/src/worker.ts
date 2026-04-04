import { isAuthorized } from './auth';
import { PoolDurableObject } from './durable-objects/pool';

export interface Env {
  FBCOORD_POOL: DurableObjectNamespace;
  FBCOORD_TOKEN: string;
}

const worker = {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);

    if (url.pathname === '/healthz') {
      return new Response('ok', { status: 200 });
    }

    if (url.pathname !== '/ws/node') {
      return new Response('not found', { status: 404 });
    }

    if (!isAuthorized(request, env.FBCOORD_TOKEN)) {
      return new Response('unauthorized', { status: 401 });
    }

    const pool = url.searchParams.get('pool')?.trim();
    if (!pool) {
      return new Response('missing pool', { status: 400 });
    }

    const durableObjectId = env.FBCOORD_POOL.idFromName(pool);
    const stub = env.FBCOORD_POOL.get(durableObjectId);
    return stub.fetch(request);
  }
};

export default worker;
export { PoolDurableObject };
