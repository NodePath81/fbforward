export interface HelloMessage {
  type: 'hello';
}

export interface ByeMessage {
  type: 'bye';
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

export interface ReadyMessage {
  type: 'ready';
  node_id: string;
}

export interface ClosingMessage {
  type: 'closing';
}

export interface ErrorMessage {
  type: 'error';
  code: string;
  message: string;
}

export type NodeInboundMessage = HelloMessage | ByeMessage | PreferencesMessage | HeartbeatMessage;
