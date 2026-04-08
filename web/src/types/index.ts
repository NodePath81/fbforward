export type Mode = 'auto' | 'manual' | 'coordination';

export interface ControlState {
  mode: Mode;
  selectedUpstream: string | null;
  isTransitioning: boolean;
}

export interface CoordinationStatus {
  available: boolean;
  connected: boolean;
  pool: string;
  node_id: string;
  selected_upstream: string;
  version: number;
  fallback_active: boolean;
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
  rttTcp: number;
  rttUdp: number;
  jitter: number;
  loss: number;
  lossRate: number;
  retransRate: number;
  score: number;
  scoreTcp: number;
  scoreUdp: number;
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
  createdAt: number;
  rateUp: number;
  rateDown: number;
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
  | 'GetGeoIPStatus'
  | 'RefreshGeoIP'
  | 'GetIPLogStatus'
  | 'QueryIPLog';

export interface RPCResponse<T = unknown> {
  ok: boolean;
  result?: T;
  error?: string;
}

export interface StatusResponse {
  mode: Mode;
  active_upstream: string;
  upstreams: UpstreamSnapshot[];
  coordination: CoordinationStatus;
}

export interface IdentityResponse {
  hostname: string;
  ips: string[];
  version?: string;
}

export interface GeoIPDBStatus {
  configured: boolean;
  available: boolean;
  path: string;
  file_mod_time: number;
  file_size: number;
}

export interface GeoIPStatusResponse {
  asn_db: GeoIPDBStatus;
  country_db: GeoIPDBStatus;
  refresh_interval: string;
}

export interface GeoIPRefreshDBResult {
  configured: boolean;
  attempted: boolean;
  refreshed: boolean;
  previous_mod_time: number;
  current_mod_time: number;
  error: string;
}

export interface RefreshGeoIPResponse {
  asn_db: GeoIPRefreshDBResult;
  country_db: GeoIPRefreshDBResult;
}

export interface IPLogStatusResponse {
  db_path: string;
  file_size: number;
  record_count: number;
  oldest_record_at: number;
  newest_record_at: number;
  retention: string;
  prune_interval: string;
}

export type IPLogSortBy =
  | 'recorded_at'
  | 'bytes_up'
  | 'bytes_down'
  | 'bytes_total'
  | 'duration_ms';

export type IPLogSortOrder = 'asc' | 'desc';

export interface IPLogQueryParams {
  start_time?: number;
  end_time?: number;
  cidr?: string;
  asn?: number;
  country?: string;
  sort_by?: IPLogSortBy;
  sort_order?: IPLogSortOrder;
  limit?: number;
  offset?: number;
}

export interface IPLogRecord {
  id: number;
  ip: string;
  asn: number;
  as_org: string;
  country: string;
  protocol: string;
  upstream: string;
  port: number;
  bytes_up: number;
  bytes_down: number;
  duration_ms: number;
  recorded_at: number;
}

export interface IPLogQueryResult {
  total: number;
  records: IPLogRecord[];
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
  duration_ms?: number;
  success?: boolean;
  rtt_ms?: number;
  jitter_ms?: number;
  loss_rate?: number;
  retrans_rate?: number;
  error?: string;
  code?: string;
  message?: string;
  depth?: number;
  next_due_ms?: number;
  running?: Array<{
    upstream: string;
    protocol: 'tcp' | 'udp';
    elapsed_ms: number;
  }>;
  pending?: Array<{
    upstream: string;
    protocol: 'tcp' | 'udp';
    scheduled_at: number;
  }>;
}
