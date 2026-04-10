import type {
  CaptureMessage,
  CreateNodeTokenResponse,
  NodeTokenInfo,
  NotificationEvent,
  OperatorTokenInfo,
  ProviderTargetSummary,
  RouteSummary,
  TestSendResponse,
  TokenRotateResponse
} from './types.js';

interface NodeTokensResponse {
  tokens: NodeTokenInfo[];
}

interface TargetsResponse {
  targets: ProviderTargetSummary[];
}

interface RoutesResponse {
  routes: RouteSummary[];
}

interface CaptureResponse {
  messages: CaptureMessage[];
}

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number
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
    throw new ApiError(body?.error ?? response.statusText, response.status);
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

export async function logout(): Promise<void> {
  await request<{ ok: boolean }>('/api/auth/logout', {
    method: 'POST'
  });
}

export async function getTokenInfo(): Promise<OperatorTokenInfo> {
  return request<OperatorTokenInfo>('/api/token/info');
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

export async function createNodeToken(sourceService: string, sourceInstance: string): Promise<CreateNodeTokenResponse> {
  return request<CreateNodeTokenResponse>('/api/node-tokens', {
    method: 'POST',
    body: JSON.stringify({
      source_service: sourceService,
      source_instance: sourceInstance
    })
  });
}

export async function revokeNodeToken(keyId: string): Promise<void> {
  await request<{ ok: boolean }>(`/api/node-tokens/${encodeURIComponent(keyId)}`, {
    method: 'DELETE'
  });
}

export async function listTargets(): Promise<ProviderTargetSummary[]> {
  const response = await request<TargetsResponse>('/api/targets');
  return response.targets;
}

export async function createTarget(payload: Record<string, unknown>): Promise<ProviderTargetSummary> {
  return request<ProviderTargetSummary>('/api/targets', {
    method: 'POST',
    body: JSON.stringify(payload)
  });
}

export async function updateTarget(id: string, payload: Record<string, unknown>): Promise<ProviderTargetSummary> {
  return request<ProviderTargetSummary>(`/api/targets/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body: JSON.stringify(payload)
  });
}

export async function deleteTarget(id: string): Promise<void> {
  await request<{ ok: boolean }>(`/api/targets/${encodeURIComponent(id)}`, {
    method: 'DELETE'
  });
}

export async function listRoutes(): Promise<RouteSummary[]> {
  const response = await request<RoutesResponse>('/api/routes');
  return response.routes;
}

export async function createRoute(payload: Record<string, unknown>): Promise<RouteSummary> {
  return request<RouteSummary>('/api/routes', {
    method: 'POST',
    body: JSON.stringify(payload)
  });
}

export async function updateRoute(id: string, payload: Record<string, unknown>): Promise<RouteSummary> {
  return request<RouteSummary>(`/api/routes/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body: JSON.stringify(payload)
  });
}

export async function deleteRoute(id: string): Promise<void> {
  await request<{ ok: boolean }>(`/api/routes/${encodeURIComponent(id)}`, {
    method: 'DELETE'
  });
}

export async function testSend(event: NotificationEvent, targetIds: string[]): Promise<TestSendResponse> {
  return request<TestSendResponse>('/api/test-send', {
    method: 'POST',
    body: JSON.stringify({
      event,
      target_ids: targetIds
    })
  });
}

export async function listCaptureMessages(): Promise<CaptureMessage[]> {
  const response = await request<CaptureResponse>('/api/capture/messages');
  return response.messages;
}

export async function clearCaptureMessages(): Promise<void> {
  await request<{ ok: boolean }>('/api/capture/clear', {
    method: 'POST'
  });
}
