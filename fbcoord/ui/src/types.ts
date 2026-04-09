export interface PickInfo {
  version: number;
  upstream: string | null;
}

export interface NodeDetail {
  node_id: string;
  upstreams: string[];
  active_upstream: string | null;
  last_seen: number;
  connected_at: number;
}

export interface CoordinationState {
  pick: PickInfo;
  node_count: number;
  nodes: NodeDetail[];
}

export interface TokenInfo {
  masked_prefix: string;
  created_at: number;
}

export interface TokenRotateResponse extends TokenInfo {
  token?: string;
}

export interface NodeTokenInfo {
  node_id: string;
  masked_prefix: string;
  created_at: number;
  last_used_at: number | null;
}

export interface CreateNodeTokenResponse {
  token: string;
  info: NodeTokenInfo;
}
