import { selectSharedUpstream, type NodePreference } from '../coordination/selector';
import type {
  ErrorMessage,
  HeartbeatMessage,
  HelloMessage,
  NodeInboundMessage,
  PickMessage,
  PreferencesMessage
} from '../protocol/types';
import { MAX_UPSTREAMS, validateNodeId, validatePoolName, validateUpstreamTag } from '../validation';

const DEFAULT_HEARTBEAT_INTERVAL_MS = 10_000;
const DEFAULT_STALE_AFTER_MS = DEFAULT_HEARTBEAT_INTERVAL_MS * 3;
const REGISTRY_OBJECT_NAME = 'global';
const MESSAGE_WINDOW_MS = 5_000;
const MAX_MESSAGES_PER_WINDOW = 10;
const RECONNECT_THROTTLE_MS = 5_000;

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

export class ConnectionRateLimiter {
  private readonly timestamps: number[] = [];

  constructor(
    private readonly now: () => number = () => Date.now(),
    private readonly windowMs: number = MESSAGE_WINDOW_MS,
    private readonly maxMessages: number = MAX_MESSAGES_PER_WINDOW
  ) {}

  allow(): boolean {
    const now = this.now();
    while (this.timestamps.length > 0 && now - (this.timestamps[0] ?? 0) > this.windowMs) {
      this.timestamps.shift();
    }
    if (this.timestamps.length >= this.maxMessages) {
      return false;
    }
    this.timestamps.push(now);
    return true;
  }
}

function parseString(value: unknown): string | null {
  return typeof value === 'string' ? value : null;
}

export function parseNodeInboundMessage(raw: unknown, expectedPool: string): { message?: NodeInboundMessage; error?: string; close: boolean } {
  if (!raw || typeof raw !== 'object') {
    return { error: 'invalid message payload', close: true };
  }

  const type = parseString((raw as { type?: unknown }).type);
  if (!type) {
    return { error: 'missing message type', close: true };
  }

  if (type === 'hello') {
    const pool = parseString((raw as { pool?: unknown }).pool);
    const nodeId = parseString((raw as { node_id?: unknown }).node_id);
    if (!pool || !nodeId) {
      return { error: 'hello requires pool and node_id', close: true };
    }
    const poolError = validatePoolName(pool);
    if (poolError) {
      return { error: poolError, close: true };
    }
    if (pool !== expectedPool) {
      return { error: 'Pool mismatch', close: true };
    }
    const nodeError = validateNodeId(nodeId);
    if (nodeError) {
      return { error: nodeError, close: true };
    }
    return {
      message: {
        type: 'hello',
        pool,
        node_id: nodeId.trim()
      },
      close: false
    };
  }

  if (type === 'preferences') {
    const upstreams = (raw as { upstreams?: unknown }).upstreams;
    const activeUpstream = (raw as { active_upstream?: unknown }).active_upstream;
    if (!Array.isArray(upstreams)) {
      return { error: 'preferences requires an upstream array', close: true };
    }
    if (upstreams.length > MAX_UPSTREAMS) {
      return { error: `upstreams must contain at most ${MAX_UPSTREAMS} entries`, close: true };
    }

    const normalizedUpstreams: string[] = [];
    for (const entry of upstreams) {
      const upstream = parseString(entry);
      if (!upstream) {
        return { error: 'upstreams must contain only strings', close: true };
      }
      const upstreamError = validateUpstreamTag(upstream);
      if (upstreamError) {
        return { error: upstreamError, close: true };
      }
      normalizedUpstreams.push(upstream.trim());
    }

    if (activeUpstream !== undefined && activeUpstream !== null) {
      const active = parseString(activeUpstream);
      if (!active) {
        return { error: 'active_upstream must be a string or null', close: true };
      }
      const upstreamError = validateUpstreamTag(active);
      if (upstreamError) {
        return { error: upstreamError, close: true };
      }
      if (!normalizedUpstreams.includes(active.trim())) {
        return { error: 'active_upstream must be present in upstreams', close: true };
      }
      return {
        message: {
          type: 'preferences',
          upstreams: normalizedUpstreams,
          active_upstream: active.trim()
        },
        close: false
      };
    }

    return {
      message: {
        type: 'preferences',
        upstreams: normalizedUpstreams,
        active_upstream: activeUpstream ?? null
      },
      close: false
    };
  }

  if (type === 'heartbeat') {
    return { message: { type: 'heartbeat' } satisfies HeartbeatMessage, close: false };
  }

  return { error: 'unknown message type', close: true };
}

export class PoolState {
  private readonly nodes = new Map<string, PoolNodeState>();
  private readonly connections = new Map<string, ConnectionLike>();
  private readonly lastReplacementAt = new Map<string, number>();
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
  ): { previous?: ConnectionLike; changed: boolean; throttled: boolean } {
    const created = !this.nodes.has(nodeId);
    const previous = this.connections.get(nodeId);
    const now = this.now();
    if (previous && previous !== connection) {
      const lastReplacementAt = this.lastReplacementAt.get(nodeId);
      if (lastReplacementAt !== undefined && now - lastReplacementAt < RECONNECT_THROTTLE_MS) {
        return {
          previous,
          changed: false,
          throttled: true
        };
      }
      this.lastReplacementAt.set(nodeId, now);
    }

    const node = this.ensureNode(nodeId);
    node.connectedAt = now;
    node.lastSeen = now;
    this.connections.set(nodeId, connection);
    return {
      previous,
      changed: created ? this.recompute() : false,
      throttled: false
    };
  }

  removeNode(nodeId: string): boolean {
    const removedNode = this.nodes.delete(nodeId);
    this.connections.delete(nodeId);
    this.lastReplacementAt.delete(nodeId);
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
        this.lastReplacementAt.delete(nodeId);
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
    const messageLimiter = new ConnectionRateLimiter();

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
        messageLimiter,
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
    messageLimiter: ConnectionRateLimiter,
    sendPick: (target: WebSocket) => void,
    broadcastPick: () => void,
    sendError: (code: string, message: string) => void,
    getNodeId: () => string | null,
    setNodeId: (nodeId: string | null) => void
  ): Promise<void> {
    if (!messageLimiter.allow()) {
      sendError('rate_limited', 'Too many messages');
      socket.close(1008, 'rate_limited');
      return;
    }

    let rawMessage: unknown;
    try {
      rawMessage = JSON.parse(String(event.data));
    } catch {
      sendError('invalid_json', 'Invalid JSON payload');
      socket.close(1008, 'invalid_json');
      return;
    }

    const parsed = parseNodeInboundMessage(rawMessage, expectedPool);
    if (!parsed.message) {
      sendError('invalid_message', parsed.error ?? 'Invalid message');
      if (parsed.close) {
        socket.close(1008, 'invalid_message');
      }
      return;
    }
    const message = parsed.message;

    if (message.type === 'hello') {
      const beforeCount = this.state.nodeCount();
      const { previous: replaced, changed, throttled } = this.state.registerConnection(message.node_id, socket);
      if (throttled) {
        sendError('reconnect_throttled', 'node reconnect is temporarily throttled');
        socket.close(1013, 'reconnect_throttled');
        return;
      }
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
      socket.close(1008, 'missing_hello');
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
