import type { ControlState, ConnectionEntry, Mode, UpstreamMetrics, UpstreamSnapshot } from '../types';

export interface AppState {
  token: string;
  control: ControlState;
  upstreams: UpstreamSnapshot[];
  metrics: Record<string, UpstreamMetrics>;
  connections: {
    tcp: Map<string, ConnectionEntry>;
    udp: Map<string, ConnectionEntry>;
  };
  counts: {
    tcp: number;
    udp: number;
  };
  memoryBytes: number;
  mode: Mode;
  activeUpstream: string;
}

type Listener = (state: AppState, prev: AppState) => void;

export class Store {
  private state: AppState;
  private listeners: Set<Listener> = new Set();

  constructor(initial: AppState) {
    this.state = initial;
  }

  getState(): AppState {
    return this.state;
  }

  setState(partial: Partial<AppState>): void {
    const prev = this.state;
    this.state = { ...prev, ...partial };
    this.emit(prev);
  }

  update(mutator: (state: AppState) => void): void {
    const prev = this.state;
    const next: AppState = {
      ...prev,
      connections: {
        tcp: new Map(prev.connections.tcp),
        udp: new Map(prev.connections.udp)
      }
    };
    mutator(next);
    this.state = next;
    this.emit(prev);
  }

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  private emit(prev: AppState): void {
    for (const listener of this.listeners) {
      listener(this.state, prev);
    }
  }
}

export function createInitialState(token: string): AppState {
  return {
    token,
    control: {
      mode: 'auto',
      selectedUpstream: null,
      isTransitioning: false
    },
    upstreams: [],
    metrics: {},
    connections: {
      tcp: new Map(),
      udp: new Map()
    },
    counts: {
      tcp: 0,
      udp: 0
    },
    memoryBytes: Number.NaN,
    mode: 'auto',
    activeUpstream: ''
  };
}
