import type { PoolDetail, PoolSummary, TokenInfo, TokenRotateResponse } from './types.js';

interface PoolsResponse {
  pools: PoolSummary[];
}

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly retryAfterSeconds: number | null = null
  ) {
    super(message);
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  if (init?.body && !headers.has('content-type')) {
    headers.set('content-type', 'application/json');
  }

  const response = await fetch(path, {
    credentials: 'same-origin',
    ...init,
    headers
  });

  let body: { error?: string } | null = null;
  try {
    body = await response.clone().json() as { error?: string };
  } catch {
    body = null;
  }

  if (!response.ok) {
    throw new ApiError(
      body?.error ?? response.statusText,
      response.status,
      response.headers.get('Retry-After') ? Number(response.headers.get('Retry-After')) : null
    );
  }

  return response.json() as Promise<T>;
}

export async function checkAuth(): Promise<void> {
  await request<{ ok: boolean }>('/api/auth/check');
}

export async function login(token: string): Promise<void> {
  await request<{ ok: boolean }>('/api/auth/login', {
    method: 'POST',
    body: JSON.stringify({ token })
  });
}

export async function getPools(): Promise<PoolSummary[]> {
  const response = await request<PoolsResponse>('/api/pools');
  return response.pools;
}

export async function getPool(pool: string): Promise<PoolDetail> {
  return request<PoolDetail>(`/api/pools/${encodeURIComponent(pool)}`);
}

export async function getTokenInfo(): Promise<TokenInfo> {
  return request<TokenInfo>('/api/token/info');
}

export async function rotateToken(payload: { token?: string; generate?: boolean }): Promise<TokenRotateResponse> {
  return request<TokenRotateResponse>('/api/token/rotate', {
    method: 'POST',
    body: JSON.stringify(payload)
  });
}
