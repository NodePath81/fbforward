import { fetchJSON } from './client';
import type { QueueStatus, RPCMethod, RPCResponse } from '../types';

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

interface RawQueueStatus {
  queue_depth: number;
  skipped_total: number;
  next_due: string | null;
  running: Array<{
    upstream: string;
    protocol: 'tcp' | 'udp';
    direction: 'upload' | 'download';
    elapsed_ms: number;
  }>;
  pending: Array<{
    upstream: string;
    protocol: 'tcp' | 'udp';
    direction: 'upload' | 'download';
    scheduled_at: string;
  }>;
}

export async function getQueueStatus(
  token: string,
  timeoutMs?: number
): Promise<RPCResponse<QueueStatus>> {
  const resp = await callRPC<RawQueueStatus>(token, 'GetQueueStatus', {}, timeoutMs);
  if (!resp.ok || !resp.result) {
    return resp as RPCResponse<QueueStatus>;
  }
  const raw = resp.result;
  return {
    ok: true,
    result: {
      queueDepth: raw.queue_depth,
      skippedTotal: raw.skipped_total,
      nextDue: raw.next_due ?? null,
      running: raw.running.map(item => ({
        upstream: item.upstream,
        protocol: item.protocol,
        direction: item.direction,
        elapsedMs: item.elapsed_ms
      })),
      pending: (raw.pending || []).map(item => ({
        upstream: item.upstream,
        protocol: item.protocol,
        direction: item.direction,
        scheduledAt: item.scheduled_at
      }))
    }
  };
}
