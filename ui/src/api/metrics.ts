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

  const activeTags = new Set<string>();
  let activeUpstream = '';
  for (const item of data['fbforward_active_upstream'] || []) {
    if (item.value === 1 && item.labels.upstream) {
      activeTags.add(item.labels.upstream);
      activeUpstream = item.labels.upstream;
    }
  }

  const upstreams: Record<string, UpstreamMetrics> = {};

  const ensure = (tag: string): UpstreamMetrics => {
    if (!upstreams[tag]) {
      upstreams[tag] = {
        rtt: 0,
        jitter: 0,
        loss: 0,
        score: 0,
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

  for (const item of data['fbforward_upstream_loss'] || []) {
    const tag = item.labels.upstream;
    if (!tag) {
      continue;
    }
    ensure(tag).loss = item.value;
  }

  for (const item of data['fbforward_upstream_score'] || []) {
    const tag = item.labels.upstream;
    if (!tag) {
      continue;
    }
    ensure(tag).score = item.value;
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

  return {
    mode,
    activeUpstream,
    counts: { tcp, udp },
    upstreams
  };
}
