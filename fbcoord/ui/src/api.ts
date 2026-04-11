import type {
  CoordinationState,
  CreateNodeTokenResponse,
  NotifyConfigInfo,
  NodeTokenInfo,
  TokenInfo,
  TokenRotateResponse
} from './types.js';

interface NodeTokensResponse {
  tokens: NodeTokenInfo[];
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

export async function getState(): Promise<CoordinationState> {
  return request<CoordinationState>('/api/state');
}

export async function getTokenInfo(): Promise<TokenInfo> {
  return request<TokenInfo>('/api/token/info');
}

export async function getNotifyConfig(): Promise<NotifyConfigInfo> {
  return request<NotifyConfigInfo>('/api/notify/config');
}

export async function updateNotifyConfig(payload: {
  endpoint: string;
  key_id: string;
  token: string;
  source_instance: string;
}): Promise<NotifyConfigInfo> {
  return request<NotifyConfigInfo>('/api/notify/config', {
    method: 'PUT',
    body: JSON.stringify(payload)
  });
}

export async function rotateToken(payload: {
  current_token: string;
  token?: string;
  generate?: boolean;
}): Promise<TokenRotateResponse> {
  return request<TokenRotateResponse>('/api/token/rotate', {
    method: 'POST',
    body: JSON.stringify(payload)
  });
}

export async function listNodeTokens(): Promise<NodeTokenInfo[]> {
  const response = await request<NodeTokensResponse>('/api/node-tokens');
  return response.tokens;
}

export async function createNodeToken(nodeId: string): Promise<CreateNodeTokenResponse> {
  return request<CreateNodeTokenResponse>('/api/node-tokens', {
    method: 'POST',
    body: JSON.stringify({ node_id: nodeId })
  });
}

export async function revokeNodeToken(nodeId: string): Promise<void> {
  await request<{ ok: boolean }>(`/api/node-tokens/${encodeURIComponent(nodeId)}`, {
    method: 'DELETE'
  });
}
