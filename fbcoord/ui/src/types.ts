export interface PickInfo {
  version: number;
  upstream: string | null;
}

export interface NodeDetail {
  node_id: string;
  status: 'online' | 'offline' | 'aborted' | 'never_seen';
  first_seen_at: number | null;
  last_connected_at: number | null;
  last_seen_at: number | null;
  disconnected_at: number | null;
  upstreams: string[];
  active_upstream: string | null;
}

export interface CoordinationState {
  pick: PickInfo;
  node_count: number;
  counts: {
    online: number;
    offline: number;
    aborted: number;
    never_seen: number;
  };
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

export interface NotifyConfigInfo {
  configured: boolean;
  source: 'stored' | 'bootstrap-env' | 'none';
  endpoint: string;
  key_id: string;
  source_instance: string;
  masked_prefix: string;
  updated_at: number | null;
  missing: string[];
}
