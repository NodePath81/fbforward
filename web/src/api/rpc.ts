import { fetchJSON } from './client';
import type { RPCMethod, RPCResponse } from '../types';

export async function callRPC<T>(
  token: string,
  method: RPCMethod,
  params: unknown,
  timeoutMs?: number
): Promise<RPCResponse<T>> {
  try {
    const res = await fetchJSON<RPCResponse<T>>(
      '/rpc',
      token,
      {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({ method, params })
      },
      timeoutMs
    );
    return res;
  } catch (err) {
    return { ok: false, error: err instanceof Error ? err.message : 'network error' };
  }
}

export async function runMeasurement(
  token: string,
  tag: string,
  protocol: 'tcp' | 'udp'
): Promise<RPCResponse<void>> {
  return callRPC<void>(token, 'RunMeasurement', { tag, protocol });
}

export async function getRuntimeConfig(
  token: string
): Promise<RPCResponse<Record<string, unknown>>> {
  return callRPC<Record<string, unknown>>(token, 'GetRuntimeConfig', {});
}
