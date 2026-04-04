import { selectSharedUpstream, type NodePreference } from '../coordination/selector';
import type { ErrorMessage, HelloMessage, PickMessage, PreferencesMessage } from '../protocol/types';

const DEFAULT_HEARTBEAT_INTERVAL_MS = 10_000;
const DEFAULT_STALE_AFTER_MS = DEFAULT_HEARTBEAT_INTERVAL_MS * 3;

type ConnectionLike = Pick<WebSocket, 'close' | 'send'>;

interface PoolNodeState extends NodePreference {
  activeUpstream: string | null;
  lastSeen: number;
}

export interface PoolSnapshot {
  version: number;
  upstream: string | null;
}

export class PoolState {
  private readonly nodes = new Map<string, PoolNodeState>();
  private readonly connections = new Map<string, ConnectionLike>();
  private snapshot: PoolSnapshot = { version: 0, upstream: null };

  constructor(
    private readonly now: () => number = () => Date.now(),
    private readonly staleAfterMs: number = DEFAULT_STALE_AFTER_MS
  ) {}

  currentPick(): PoolSnapshot {
    return { ...this.snapshot };
  }

  registerConnection(
    nodeId: string,
    connection: ConnectionLike
  ): { previous?: ConnectionLike; changed: boolean } {
    const created = !this.nodes.has(nodeId);
    this.ensureNode(nodeId);
    const previous = this.connections.get(nodeId);
    this.connections.set(nodeId, connection);
    return {
      previous,
      changed: created ? this.recompute() : false
    };
  }

  removeNode(nodeId: string): boolean {
    const removedNode = this.nodes.delete(nodeId);
    this.connections.delete(nodeId);
    if (!removedNode) {
      return false;
    }
    return this.recompute();
  }

  setPreferences(nodeId: string, upstreams: string[], activeUpstream: string | null): boolean {
    const node = this.ensureNode(nodeId);
    node.upstreams = [...upstreams];
    node.activeUpstream = activeUpstream;
    node.lastSeen = this.now();
    return this.recompute();
  }

  heartbeat(nodeId: string): boolean {
    const node = this.ensureNode(nodeId);
    node.lastSeen = this.now();
    return this.reapStaleNodes();
  }

  reapStaleNodes(): boolean {
    const cutoff = this.now() - this.staleAfterMs;
    let removed = false;
    for (const [nodeId, node] of this.nodes.entries()) {
      if (node.lastSeen < cutoff) {
        this.nodes.delete(nodeId);
        this.connections.delete(nodeId);
        removed = true;
      }
    }
    if (!removed) {
      return false;
    }
    return this.recompute();
  }

  connectionsList(): ConnectionLike[] {
    return Array.from(this.connections.values());
  }

  private ensureNode(nodeId: string): PoolNodeState {
    const existing = this.nodes.get(nodeId);
    if (existing) {
      return existing;
    }
    const created: PoolNodeState = {
      nodeId,
      upstreams: [],
      activeUpstream: null,
      lastSeen: this.now()
    };
    this.nodes.set(nodeId, created);
    return created;
  }

  private recompute(): boolean {
    const upstream = selectSharedUpstream(Array.from(this.nodes.values()));
    if (upstream === this.snapshot.upstream) {
      return false;
    }
    this.snapshot = {
      version: this.snapshot.version + 1,
      upstream
    };
    return true;
  }
}

export class PoolDurableObject {
  private readonly state = new PoolState();
  private readonly socketNodes = new Map<WebSocket, string>();

  constructor(_state: DurableObjectState) {}

  async fetch(request: Request): Promise<Response> {
    if (request.headers.get('upgrade') !== 'websocket') {
      return new Response('websocket upgrade required', { status: 426 });
    }

    const expectedPool = new URL(request.url).searchParams.get('pool')?.trim();
    if (!expectedPool) {
      return new Response('missing pool', { status: 400 });
    }

    const pair = new WebSocketPair();
    const client = pair[0];
    const server = pair[1];
    server.accept();
    this.attach(server, expectedPool);

    return new Response(null, {
      status: 101,
      webSocket: client
    } as ResponseInit & { webSocket: WebSocket });
  }

  private attach(socket: WebSocket, expectedPool: string): void {
    let nodeId: string | null = null;

    const sendPick = (target: WebSocket): void => {
      const snapshot = this.state.currentPick();
      const payload: PickMessage = {
        type: 'pick',
        version: snapshot.version,
        upstream: snapshot.upstream
      };
      target.send(JSON.stringify(payload));
    };

    const broadcastPick = (): void => {
      for (const connection of this.state.connectionsList()) {
        connection.send(JSON.stringify({
          type: 'pick',
          version: this.state.currentPick().version,
          upstream: this.state.currentPick().upstream
        } satisfies PickMessage));
      }
    };

    const sendError = (code: string, message: string): void => {
      const payload: ErrorMessage = { type: 'error', code, message };
      socket.send(JSON.stringify(payload));
    };

    const closeNode = (): void => {
      if (!nodeId) {
        return;
      }
      this.socketNodes.delete(socket);
      if (this.state.removeNode(nodeId)) {
        broadcastPick();
      }
      nodeId = null;
    };

    socket.addEventListener('message', event => {
      let message: HelloMessage | PreferencesMessage | { type: 'heartbeat' };
      try {
        message = JSON.parse(String(event.data));
      } catch {
        sendError('invalid_json', 'Invalid JSON payload');
        return;
      }

      if (message.type === 'hello') {
        if (message.pool !== expectedPool) {
          sendError('invalid_pool', 'Pool mismatch');
          socket.close(1008, 'invalid_pool');
          return;
        }
        nodeId = message.node_id;
        const { previous: replaced, changed } = this.state.registerConnection(nodeId, socket);
        this.socketNodes.set(socket, nodeId);
        if (replaced && replaced !== socket) {
          replaced.close(1012, 'replaced');
        }
        if (changed || this.state.reapStaleNodes()) {
          broadcastPick();
        }
        sendPick(socket);
        return;
      }

      if (!nodeId) {
        sendError('missing_hello', 'hello must be sent first');
        return;
      }

      if (message.type === 'preferences') {
        if (this.state.setPreferences(nodeId, message.upstreams, message.active_upstream ?? null)) {
          broadcastPick();
        }
        return;
      }

      if (message.type === 'heartbeat') {
        if (this.state.heartbeat(nodeId)) {
          broadcastPick();
        }
      }
    });

    socket.addEventListener('close', closeNode);
    socket.addEventListener('error', closeNode);
  }
}
