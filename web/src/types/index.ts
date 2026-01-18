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
  bandwidth_up_bps: number;
  bandwidth_down_bps: number;
  rtt_ms: number;
  jitter_ms: number;
  retrans_rate: number;
  loss_rate: number;
  loss: number;
  score_tcp: number;
  score_udp: number;
  score: number;
  utilization: number;
  last_tcp_update?: string;
  last_udp_update?: string;
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
  utilization: number;
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
  last_activity: number;
  age: number;
  kind: 'tcp' | 'udp';
}

export type RPCMethod = 'GetStatus' | 'SetUpstream' | 'Restart' | 'ListUpstreams' | 'GetMeasurementConfig';

export interface RPCResponse<T = unknown> {
  ok: boolean;
  result?: T;
  error?: string;
}

export interface StatusResponse {
  mode: Mode;
  active_upstream: string;
  counts: {
    tcp_active: number;
    udp_active: number;
  };
  upstreams: UpstreamSnapshot[];
}

export interface IdentityResponse {
  hostname: string;
  ips: string[];
  version?: string;
}

export type WSMessageType = 'snapshot' | 'add' | 'update' | 'remove';

export interface WSMessage {
  type: WSMessageType;
  tcp?: RawConnectionEntry[];
  udp?: RawConnectionEntry[];
  entry?: RawConnectionEntry;
  kind?: 'tcp' | 'udp';
  id?: string;
}
