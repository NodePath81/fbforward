import type { Mode, UpstreamMetrics } from '../types';

export interface MetricSample {
  labels: Record<string, string>;
  value: number;
}

export type MetricsMap = Record<string, MetricSample[]>;

export interface MetricsSnapshot {
  mode: Mode;
  activeUpstream: string;
  counts: {
    tcp: number;
    udp: number;
  };
  upstreams: Record<string, UpstreamMetrics>;
  memoryBytes: number;
  uptimeSeconds: number;
  totalBytesUp: number;
  totalBytesDown: number;
}

export function parseMetrics(text: string): MetricsMap {
  const lines = text.split('\n');
  const metrics: MetricsMap = {};
  for (const line of lines) {
    if (!line || line.startsWith('#')) {
      continue;
    }
    const match = line.match(/^([a-zA-Z0-9_:]+)(\{[^}]*\})?\s+([0-9eE+\-\.]+)/);
    if (!match) {
      continue;
    }
    const name = match[1];
    const labelsRaw = match[2] || '';
    const value = Number.parseFloat(match[3]);
    const labels: Record<string, string> = {};
    if (labelsRaw) {
      const parts = labelsRaw.slice(1, -1).split(',');
      for (const part of parts) {
        const [key, rawValue] = part.split('=');
        if (!key) {
          continue;
        }
        labels[key] = (rawValue || '').replace(/"/g, '');
      }
    }
    if (!metrics[name]) {
      metrics[name] = [];
    }
    metrics[name].push({ labels, value });
  }
  return metrics;
}

export function extractMetrics(data: MetricsMap): MetricsSnapshot {
  const modeValue = data['fbforward_mode']?.[0]?.value ?? 0;
  const mode: Mode = modeValue === 1 ? 'manual' : 'auto';

  const tcp = data['fbforward_tcp_active']?.[0]?.value ?? 0;
  const udp = data['fbforward_udp_mappings_active']?.[0]?.value ?? 0;
  const memorySample = data['fbforward_memory_alloc_bytes']?.[0]?.value;
  const memoryBytes = Number.isFinite(memorySample) ? memorySample : Number.NaN;
  const uptimeSample = data['fbforward_uptime_seconds']?.[0]?.value;
  const uptimeSeconds = Number.isFinite(uptimeSample) ? uptimeSample : Number.NaN;

  const activeTags = new Set<string>();
  let activeUpstream = '';
  for (const item of data['fbforward_active_upstream'] || []) {
    if (item.value === 1 && item.labels.upstream) {
      activeTags.add(item.labels.upstream);
      activeUpstream = item.labels.upstream;
    }
  }

  const upstreams: Record<string, UpstreamMetrics> = {};
  let totalBytesUp = 0;
  let totalBytesDown = 0;

  const ensure = (tag: string): UpstreamMetrics => {
    if (!upstreams[tag]) {
      upstreams[tag] = {
        rtt: 0,
        jitter: 0,
        loss: 0,
        lossRate: 0,
        retransRate: 0,
        score: 0,
        scoreTcp: 0,
        scoreUdp: 0,
        scoreOverall: 0,
        bandwidthUpBps: 0,
        bandwidthDownBps: 0,
        utilization: 0,
        reachable: false,
        unusable: true,
        active: activeTags.has(tag)
      };
    }
    return upstreams[tag];
  };

  for (const item of data['fbforward_upstream_rtt_ms'] || []) {
    const tag = item.labels.upstream;
    if (!tag) {
      continue;
    }
    ensure(tag).rtt = item.value;
  }

  for (const item of data['fbforward_upstream_jitter_ms'] || []) {
    const tag = item.labels.upstream;
    if (!tag) {
      continue;
    }
    ensure(tag).jitter = item.value;
  }

  for (const item of data['fbforward_upstream_bandwidth_up_bps'] || []) {
    const tag = item.labels.upstream;
    if (!tag) {
      continue;
    }
    ensure(tag).bandwidthUpBps = item.value;
  }

  for (const item of data['fbforward_upstream_bandwidth_down_bps'] || []) {
    const tag = item.labels.upstream;
    if (!tag) {
      continue;
    }
    ensure(tag).bandwidthDownBps = item.value;
  }

  for (const item of data['fbforward_upstream_retrans_rate'] || []) {
    const tag = item.labels.upstream;
    if (!tag) {
      continue;
    }
    ensure(tag).retransRate = item.value;
  }

  for (const item of data['fbforward_upstream_loss_rate'] || []) {
    const tag = item.labels.upstream;
    if (!tag) {
      continue;
    }
    ensure(tag).lossRate = item.value;
  }

  for (const item of data['fbforward_upstream_loss'] || []) {
    const tag = item.labels.upstream;
    if (!tag) {
      continue;
    }
    ensure(tag).loss = item.value;
  }

  for (const item of data['fbforward_upstream_score_tcp'] || []) {
    const tag = item.labels.upstream;
    if (!tag) {
      continue;
    }
    ensure(tag).scoreTcp = item.value;
  }

  for (const item of data['fbforward_upstream_score_udp'] || []) {
    const tag = item.labels.upstream;
    if (!tag) {
      continue;
    }
    ensure(tag).scoreUdp = item.value;
  }

  for (const item of data['fbforward_upstream_score_overall'] || []) {
    const tag = item.labels.upstream;
    if (!tag) {
      continue;
    }
    ensure(tag).scoreOverall = item.value;
  }

  for (const item of data['fbforward_upstream_score'] || []) {
    const tag = item.labels.upstream;
    if (!tag) {
      continue;
    }
    ensure(tag).score = item.value;
  }

  for (const item of data['fbforward_upstream_utilization'] || []) {
    const tag = item.labels.upstream;
    if (!tag) {
      continue;
    }
    ensure(tag).utilization = item.value;
  }

  for (const item of data['fbforward_upstream_reachable'] || []) {
    const tag = item.labels.upstream;
    if (!tag) {
      continue;
    }
    ensure(tag).reachable = item.value === 1;
  }

  for (const item of data['fbforward_upstream_unusable'] || []) {
    const tag = item.labels.upstream;
    if (!tag) {
      continue;
    }
    ensure(tag).unusable = item.value === 1;
  }

  for (const tag of Object.keys(upstreams)) {
    upstreams[tag].active = activeTags.has(tag);
  }

  for (const item of data['fbforward_bytes_up_total'] || []) {
    if (Number.isFinite(item.value)) {
      totalBytesUp += item.value;
    }
  }

  for (const item of data['fbforward_bytes_down_total'] || []) {
    if (Number.isFinite(item.value)) {
      totalBytesDown += item.value;
    }
  }

  return {
    mode,
    activeUpstream,
    counts: { tcp, udp },
    upstreams,
    memoryBytes,
    uptimeSeconds,
    totalBytesUp,
    totalBytesDown
  };
}
