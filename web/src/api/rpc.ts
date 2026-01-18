import { fetchJSON } from './client';
import type { RPCMethod, RPCResponse } from '../types';

export async function callRPC<T>(token: string, method: RPCMethod, params: unknown): Promise<RPCResponse<T>> {
  try {
    const res = await fetchJSON<RPCResponse<T>>('/rpc', token, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ method, params })
    });
    return res;
  } catch (err) {
    return { ok: false, error: 'network error' };
  }
}

export async function runMeasurement(
  token: string,
  tag: string,
  protocol: 'tcp' | 'udp'
): Promise<RPCResponse<void>> {
  return callRPC<void>(token, 'RunMeasurement', { tag, protocol });
}
