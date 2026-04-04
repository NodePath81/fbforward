import { selectSharedUpstream, type NodePreference } from '../coordination/selector';
import type {
  ErrorMessage,
  HeartbeatMessage,
  HelloMessage,
  PickMessage,
  PreferencesMessage
} from '../protocol/types';

const DEFAULT_HEARTBEAT_INTERVAL_MS = 10_000;
const DEFAULT_STALE_AFTER_MS = DEFAULT_HEARTBEAT_INTERVAL_MS * 3;
const REGISTRY_OBJECT_NAME = 'global';

type ConnectionLike = Pick<WebSocket, 'close' | 'send'>;

interface PoolNodeState extends NodePreference {
  activeUpstream: string | null;
  lastSeen: number;
  connectedAt: number;
}

export interface PoolNodeSnapshot {
  node_id: string;
  upstreams: string[];
  active_upstream: string | null;
  last_seen: number;
  connected_at: number;
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

  nodeCount(): number {
    return this.nodes.size;
  }

  currentPick(): PoolSnapshot {
    return { ...this.snapshot };
  }

  nodeSnapshot(): PoolNodeSnapshot[] {
    return Array.from(this.nodes.values())
      .map(node => ({
        node_id: node.nodeId,
        upstreams: [...node.upstreams],
        active_upstream: node.activeUpstream,
        last_seen: node.lastSeen,
        connected_at: node.connectedAt
      }))
      .sort((left, right) => left.node_id.localeCompare(right.node_id));
  }

  registerConnection(
    nodeId: string,
    connection: ConnectionLike
  ): { previous?: ConnectionLike; changed: boolean } {
    const created = !this.nodes.has(nodeId);
    const node = this.ensureNode(nodeId);
    const now = this.now();
    node.connectedAt = now;
    node.lastSeen = now;
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
      lastSeen: this.now(),
      connectedAt: this.now()
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
  private readonly registry?: DurableObjectNamespace;

  constructor(
    _state: DurableObjectState,
    env?: { FBCOORD_REGISTRY?: DurableObjectNamespace }
  ) {
    this.registry = env?.FBCOORD_REGISTRY;
  }

  async fetch(request: Request): Promise<Response> {
    const url = new URL(request.url);
    const expectedPool = url.searchParams.get('pool')?.trim();
    if (!expectedPool) {
      return new Response('missing pool', { status: 400 });
    }

    if (request.method === 'GET' && url.pathname === '/state') {
      return Response.json({
        pool: expectedPool,
        pick: this.state.currentPick(),
        node_count: this.state.nodeCount(),
        nodes: this.state.nodeSnapshot()
      });
    }

    if (request.headers.get('upgrade') !== 'websocket') {
      return new Response('websocket upgrade required', { status: 426 });
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

    const closeNode = async (): Promise<void> => {
      if (!nodeId) {
        return;
      }
      const beforeCount = this.state.nodeCount();
      if (this.state.removeNode(nodeId)) {
        broadcastPick();
      }
      await this.syncRegistry(expectedPool, beforeCount, this.state.nodeCount());
      nodeId = null;
    };

    socket.addEventListener('message', event => {
      void this.handleMessage(
        event,
        expectedPool,
        socket,
        sendPick,
        broadcastPick,
        sendError,
        () => nodeId,
        value => {
          nodeId = value;
        }
      );
    });

    socket.addEventListener('close', () => {
      void closeNode();
    });
    socket.addEventListener('error', () => {
      void closeNode();
    });
  }

  private async handleMessage(
    event: MessageEvent,
    expectedPool: string,
    socket: WebSocket,
    sendPick: (target: WebSocket) => void,
    broadcastPick: () => void,
    sendError: (code: string, message: string) => void,
    getNodeId: () => string | null,
    setNodeId: (nodeId: string | null) => void
  ): Promise<void> {
    let message: HelloMessage | PreferencesMessage | HeartbeatMessage;
    try {
      message = JSON.parse(String(event.data)) as HelloMessage | PreferencesMessage | HeartbeatMessage;
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

      const beforeCount = this.state.nodeCount();
      const { previous: replaced, changed } = this.state.registerConnection(message.node_id, socket);
      setNodeId(message.node_id);
      const reaped = this.state.reapStaleNodes();
      await this.syncRegistry(expectedPool, beforeCount, this.state.nodeCount());

      if (replaced && replaced !== socket) {
        replaced.close(1012, 'replaced');
      }
      if (changed || reaped) {
        broadcastPick();
      }
      sendPick(socket);
      return;
    }

    const nodeId = getNodeId();
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
      const beforeCount = this.state.nodeCount();
      const changed = this.state.heartbeat(nodeId);
      await this.syncRegistry(expectedPool, beforeCount, this.state.nodeCount());
      if (changed) {
        broadcastPick();
      }
    }
  }

  private async syncRegistry(pool: string, beforeCount: number, afterCount: number): Promise<void> {
    if (!this.registry || beforeCount === afterCount || (beforeCount > 0 && afterCount > 0)) {
      return;
    }

    const registryId = this.registry.idFromName(REGISTRY_OBJECT_NAME);
    const stub = this.registry.get(registryId);
    const pathname = beforeCount === 0 && afterCount > 0 ? '/register' : '/deregister';
    await stub.fetch(new Request(`https://registry.internal${pathname}`, {
      method: 'POST',
      headers: {
        'content-type': 'application/json'
      },
      body: JSON.stringify({ pool })
    }));
  }
}
