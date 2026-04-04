export interface PickInfo {
  version: number;
  upstream: string | null;
}

export interface PoolSummary {
  name: string;
  node_count: number;
  pick: PickInfo;
}

export interface NodeDetail {
  node_id: string;
  upstreams: string[];
  active_upstream: string | null;
  last_seen: number;
  connected_at: number;
}

export interface PoolDetail {
  pool: string;
  node_count: number;
  pick: PickInfo;
  nodes: NodeDetail[];
}

export interface TokenInfo {
  masked_prefix: string;
  created_at: number;
}

export interface TokenRotateResponse extends TokenInfo {
  token?: string;
}
