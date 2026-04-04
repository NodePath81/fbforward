export interface HelloMessage {
  type: 'hello';
  pool: string;
  node_id: string;
}

export interface PreferencesMessage {
  type: 'preferences';
  upstreams: string[];
  active_upstream?: string | null;
}

export interface HeartbeatMessage {
  type: 'heartbeat';
}

export interface PickMessage {
  type: 'pick';
  version: number;
  upstream: string | null;
}

export interface ErrorMessage {
  type: 'error';
  code: string;
  message: string;
}

export type NodeInboundMessage = HelloMessage | PreferencesMessage | HeartbeatMessage;
