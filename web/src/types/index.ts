export type Mode = 'auto' | 'manual';

export interface ControlState {
  mode: Mode;
  selectedUpstream: string | null;
  isTransitioning: boolean;
}

export interface UpstreamSnapshot {
  tag: string;
  host: string;
  ips: string[];
  active_ip: string;
  active: boolean;
  usable: boolean;
  reachable: boolean;
}

export interface UpstreamConfig {
  tag: string;
  host: string;
  activeIp?: string;
  ips?: string[];
}

export interface UpstreamMetrics {
  rtt: number;
  jitter: number;
  loss: number;
  lossRate: number;
  retransRate: number;
  score: number;
  scoreTcp: number;
  scoreUdp: number;
  scoreOverall: number;
  bandwidthUpBps: number;
  bandwidthDownBps: number;
  bandwidthTcpUpBps: number;
  bandwidthTcpDownBps: number;
  bandwidthUdpUpBps: number;
  bandwidthUdpDownBps: number;
  utilization: number;
  utilizationUp: number;
  utilizationDown: number;
  reachable: boolean;
  unusable: boolean;
  active: boolean;
}

export interface ConnectionEntry {
  id: string;
  clientAddr: string;
  port: number;
  upstream: string;
  bytesUp: number;
  bytesDown: number;
  segmentsUp: number;
  segmentsDown: number;
  lastActivity: number;
  age: number;
  kind: 'tcp' | 'udp';
}

export interface RawConnectionEntry {
  id: string;
  client_addr: string;
  port: number;
  upstream: string;
  bytes_up: number;
  bytes_down: number;
  segments_up: number;
  segments_down: number;
  last_activity: number;
  age: number;
  kind: 'tcp' | 'udp';
}

export type RPCMethod =
  | 'GetStatus'
  | 'SetUpstream'
  | 'Restart'
  | 'ListUpstreams'
  | 'GetMeasurementConfig'
  | 'GetRuntimeConfig'
  | 'GetScheduleStatus'
  | 'RunMeasurement';

export interface RPCResponse<T = unknown> {
  ok: boolean;
  result?: T;
  error?: string;
}

export interface StatusResponse {
  mode: Mode;
  active_upstream: string;
  upstreams: UpstreamSnapshot[];
}

export interface QueueStatus {
  queueDepth: number;
  skippedTotal: number;
  nextDue: string | null;
  running: RunningTest[];
  pending: PendingTest[];
}

export interface RunningTest {
  upstream: string;
  protocol: 'tcp' | 'udp';
  direction: 'upload' | 'download';
  elapsedMs: number;
}

export interface PendingTest {
  upstream: string;
  protocol: 'tcp' | 'udp';
  direction: 'upload' | 'download';
  scheduledAt: string;
}

export interface IdentityResponse {
  hostname: string;
  ips: string[];
  version?: string;
}

export type WSMessageType =
  | 'connections_snapshot'
  | 'queue_snapshot'
  | 'add'
  | 'update'
  | 'remove'
  | 'test_history_event'
  | 'error';

export interface WSMessage {
  schema_version: number;
  type: WSMessageType;
  timestamp?: number;
  tcp?: RawConnectionEntry[];
  udp?: RawConnectionEntry[];
  entry?: RawConnectionEntry;
  kind?: 'tcp' | 'udp';
  id?: string;
  upstream?: string;
  protocol?: 'tcp' | 'udp';
  direction?: 'upload' | 'download';
  duration_ms?: number;
  success?: boolean;
  bandwidth_up_bps?: number;
  bandwidth_down_bps?: number;
  rtt_ms?: number;
  jitter_ms?: number;
  loss_rate?: number;
  retrans_rate?: number;
  error?: string;
  code?: string;
  message?: string;
  depth?: number;
  skipped?: number;
  next_due_ms?: number;
  running?: Array<{
    upstream: string;
    protocol: 'tcp' | 'udp';
    direction: 'upload' | 'download';
    elapsed_ms: number;
  }>;
  pending?: Array<{
    upstream: string;
    protocol: 'tcp' | 'udp';
    direction: 'upload' | 'download';
    scheduled_at: number;
  }>;
}
