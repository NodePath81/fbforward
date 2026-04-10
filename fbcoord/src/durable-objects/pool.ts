import { selectSharedUpstream, type NodePreference } from '../coordination/selector';
import type {
  ByeMessage,
  ClosingMessage,
  ErrorMessage,
  HeartbeatMessage,
  HelloMessage,
  NodeInboundMessage,
  PickMessage,
  ReadyMessage
} from '../protocol/types';
import { MAX_UPSTREAMS, validateNodeId, validateUpstreamTag } from '../validation';

const DEFAULT_HEARTBEAT_INTERVAL_MS = 10_000;
const DEFAULT_STALE_AFTER_MS = DEFAULT_HEARTBEAT_INTERVAL_MS * 3;
const MESSAGE_WINDOW_MS = 5_000;
const MAX_MESSAGES_PER_WINDOW = 10;
const RECONNECT_THROTTLE_MS = 5_000;
const HELLO_TIMEOUT_MS = 5_000;
const TEARDOWN_CLOSE_TIMEOUT_MS = 2_000;
const ROSTER_KEY = 'roster';

export const AUTHENTICATED_NODE_ID_HEADER = 'x-fbcoord-node-id';

type ConnectionLike = Pick<WebSocket, 'close' | 'send'>;
type SessionPhase = 'await_hello' | 'online' | 'teardown_accepted' | 'closed';
type PoolNodeStatus = 'online' | 'offline' | 'aborted';

interface ActiveNodeState extends NodePreference {
  activeUpstream: string | null;
  lastSeen: number;
  connectedAt: number;
  connection: ConnectionLike;
}

interface StoredRosterEntry {
  status: PoolNodeStatus;
  firstSeenAt: number;
  lastConnectedAt: number;
  lastSeenAt: number;
  disconnectedAt: number | null;
}

type StoredRoster = Record<string, StoredRosterEntry>;

export interface PoolNodeSnapshot {
  node_id: string;
  status: PoolNodeStatus;
  first_seen_at: number;
  last_connected_at: number;
  last_seen_at: number;
  disconnected_at: number | null;
  upstreams: string[];
  active_upstream: string | null;
}

export interface PoolStatusCounts {
  online: number;
  offline: number;
  aborted: number;
}

export interface PoolSnapshot {
  version: number;
  upstream: string | null;
}

export interface PoolStateResponse {
  pick: PoolSnapshot;
  node_count: number;
  counts: PoolStatusCounts;
  nodes: PoolNodeSnapshot[];
}

interface MutationResult {
  changed: boolean;
  rosterChanged: boolean;
}

interface RegisterConnectionResult extends MutationResult {
  previous?: ConnectionLike;
  throttled: boolean;
}

interface ReapResult extends MutationResult {
  connectionsToClose: ConnectionLike[];
}

interface RevokeNodeResult extends MutationResult {
  connection?: ConnectionLike;
  removed: boolean;
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

function cloneStoredRoster(roster: StoredRoster): StoredRoster {
  return Object.fromEntries(
    Object.entries(roster).map(([nodeId, entry]) => [
      nodeId,
      {
        status: entry.status,
        firstSeenAt: entry.firstSeenAt,
        lastConnectedAt: entry.lastConnectedAt,
        lastSeenAt: entry.lastSeenAt,
        disconnectedAt: entry.disconnectedAt
      } satisfies StoredRosterEntry
    ])
  );
}

export function parseNodeInboundMessage(raw: unknown): { message?: NodeInboundMessage; error?: string; close: boolean } {
  if (!raw || typeof raw !== 'object') {
    return { error: 'invalid message payload', close: true };
  }

  const type = parseString((raw as { type?: unknown }).type);
  if (!type) {
    return { error: 'missing message type', close: true };
  }

  if (type === 'hello') {
    return {
      message: {
        type: 'hello'
      } satisfies HelloMessage,
      close: false
    };
  }

  if (type === 'bye') {
    return {
      message: {
        type: 'bye'
      } satisfies ByeMessage,
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
  private readonly activeNodes = new Map<string, ActiveNodeState>();
  private readonly lastReplacementAt = new Map<string, number>();
  private roster: StoredRoster = {};
  private snapshot: PoolSnapshot = { version: 0, upstream: null };

  constructor(
    private readonly now: () => number = () => Date.now(),
    private readonly staleAfterMs: number = DEFAULT_STALE_AFTER_MS
  ) {}

  hydrateRoster(roster: StoredRoster): void {
    this.roster = cloneStoredRoster(roster);
    this.snapshot = { version: 0, upstream: null };
    this.recompute();
  }

  exportRoster(): StoredRoster {
    return cloneStoredRoster(this.roster);
  }

  normalizeLoadedRoster(): boolean {
    const now = this.now();
    let changed = false;
    for (const entry of Object.values(this.roster)) {
      if (entry.status === 'online') {
        entry.status = 'aborted';
        entry.disconnectedAt = now;
        changed = true;
      }
    }
    return changed;
  }

  nodeCount(): number {
    return this.activeNodes.size;
  }

  currentPick(): PoolSnapshot {
    return { ...this.snapshot };
  }

  stateSnapshot(): PoolStateResponse {
    const counts: PoolStatusCounts = {
      online: 0,
      offline: 0,
      aborted: 0
    };

    const nodes = Object.entries(this.roster)
      .map(([nodeId, entry]) => {
        counts[entry.status] += 1;
        const active = this.activeNodes.get(nodeId);
        return {
          node_id: nodeId,
          status: entry.status,
          first_seen_at: entry.firstSeenAt,
          last_connected_at: entry.lastConnectedAt,
          last_seen_at: entry.lastSeenAt,
          disconnected_at: entry.disconnectedAt,
          upstreams: entry.status === 'online' && active ? [...active.upstreams] : [],
          active_upstream: entry.status === 'online' && active ? active.activeUpstream : null
        } satisfies PoolNodeSnapshot;
      })
      .sort((left, right) => left.node_id.localeCompare(right.node_id));

    return {
      pick: this.currentPick(),
      node_count: counts.online,
      counts,
      nodes
    };
  }

  registerConnection(nodeId: string, connection: ConnectionLike): RegisterConnectionResult {
    const current = this.activeNodes.get(nodeId);
    const now = this.now();

    if (current && current.connection !== connection) {
      const lastReplacementAt = this.lastReplacementAt.get(nodeId);
      if (lastReplacementAt !== undefined && now - lastReplacementAt < RECONNECT_THROTTLE_MS) {
        return {
          previous: current.connection,
          changed: false,
          rosterChanged: false,
          throttled: true
        };
      }
      this.lastReplacementAt.set(nodeId, now);
    }

    const previous = current?.connection;
    this.activeNodes.set(nodeId, {
      nodeId,
      upstreams: [],
      activeUpstream: null,
      lastSeen: now,
      connectedAt: now,
      connection
    });

    const existing = this.roster[nodeId];
    this.roster[nodeId] = existing
      ? {
          ...existing,
          status: 'online',
          lastConnectedAt: now,
          lastSeenAt: now,
          disconnectedAt: null
        }
      : {
          status: 'online',
          firstSeenAt: now,
          lastConnectedAt: now,
          lastSeenAt: now,
          disconnectedAt: null
        };

    return {
      previous,
      changed: this.recompute(),
      rosterChanged: true,
      throttled: false
    };
  }

  setPreferences(nodeId: string, connection: ConnectionLike, upstreams: string[], activeUpstream: string | null): MutationResult {
    const current = this.activeNodes.get(nodeId);
    if (!current || current.connection !== connection) {
      return { changed: false, rosterChanged: false };
    }

    const now = this.now();
    current.upstreams = [...upstreams];
    current.activeUpstream = activeUpstream;
    current.lastSeen = now;

    const entry = this.roster[nodeId];
    if (entry) {
      entry.lastSeenAt = now;
    }

    return {
      changed: this.recompute(),
      rosterChanged: true
    };
  }

  heartbeat(nodeId: string, connection: ConnectionLike): MutationResult {
    const current = this.activeNodes.get(nodeId);
    if (!current || current.connection !== connection) {
      return { changed: false, rosterChanged: false };
    }

    const now = this.now();
    current.lastSeen = now;

    const entry = this.roster[nodeId];
    if (entry) {
      entry.lastSeenAt = now;
    }

    return {
      changed: false,
      rosterChanged: true
    };
  }

  acceptTeardown(nodeId: string, connection: ConnectionLike): MutationResult {
    const current = this.activeNodes.get(nodeId);
    if (!current || current.connection !== connection) {
      return { changed: false, rosterChanged: false };
    }

    this.activeNodes.delete(nodeId);
    this.lastReplacementAt.delete(nodeId);

    const entry = this.roster[nodeId];
    if (entry) {
      entry.status = 'offline';
      entry.disconnectedAt = this.now();
    }

    return {
      changed: this.recompute(),
      rosterChanged: true
    };
  }

  abortConnection(nodeId: string, connection: ConnectionLike): MutationResult {
    const current = this.activeNodes.get(nodeId);
    if (!current || current.connection !== connection) {
      return { changed: false, rosterChanged: false };
    }

    this.activeNodes.delete(nodeId);
    this.lastReplacementAt.delete(nodeId);

    const entry = this.roster[nodeId];
    if (entry) {
      entry.status = 'aborted';
      entry.disconnectedAt = this.now();
    }

    return {
      changed: this.recompute(),
      rosterChanged: true
    };
  }

  reapStaleNodes(): ReapResult {
    const cutoff = this.now() - this.staleAfterMs;
    const connectionsToClose: ConnectionLike[] = [];
    let rosterChanged = false;

    for (const [nodeId, current] of this.activeNodes.entries()) {
      if (current.lastSeen >= cutoff) {
        continue;
      }

      this.activeNodes.delete(nodeId);
      this.lastReplacementAt.delete(nodeId);
      connectionsToClose.push(current.connection);

      const entry = this.roster[nodeId];
      if (entry) {
        entry.status = 'aborted';
        entry.disconnectedAt = this.now();
        rosterChanged = true;
      }
    }

    return {
      changed: connectionsToClose.length > 0 ? this.recompute() : false,
      rosterChanged,
      connectionsToClose
    };
  }

  revokeNode(nodeId: string): RevokeNodeResult {
    const current = this.activeNodes.get(nodeId);
    if (current) {
      this.activeNodes.delete(nodeId);
    }
    this.lastReplacementAt.delete(nodeId);

    const removed = delete this.roster[nodeId];
    if (!removed && !current) {
      return {
        removed: false,
        changed: false,
        rosterChanged: false
      };
    }

    return {
      removed: true,
      connection: current?.connection,
      changed: this.recompute(),
      rosterChanged: true
    };
  }

  connectionsList(): ConnectionLike[] {
    return Array.from(this.activeNodes.values()).map(node => node.connection);
  }

  private recompute(): boolean {
    const upstream = selectSharedUpstream(Array.from(this.activeNodes.values()));
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

function sendJson(socket: WebSocket, payload: ReadyMessage | PickMessage | ClosingMessage | ErrorMessage): void {
  socket.send(JSON.stringify(payload));
}

export class PoolDurableObject {
  private readonly state = new PoolState();
  private readonly storage: DurableObjectStorage;
  private loadPromise: Promise<void> | null = null;

  constructor(state: DurableObjectState) {
    this.storage = state.storage;
  }

  async fetch(request: Request): Promise<Response> {
    await this.ensureLoaded();
    const url = new URL(request.url);

    if (request.method === 'GET' && url.pathname === '/state') {
      await this.flushReapResult(this.state.reapStaleNodes());
      return Response.json(this.state.stateSnapshot());
    }

    if (request.method === 'DELETE' && url.pathname.startsWith('/nodes/')) {
      const nodeId = decodeURIComponent(url.pathname.slice('/nodes/'.length)).trim();
      const nodeIdError = validateNodeId(nodeId);
      if (nodeIdError) {
        return Response.json({ error: nodeIdError }, { status: 400 });
      }

      const result = this.state.revokeNode(nodeId);
      await this.persistIfNeeded(result.rosterChanged);
      if (result.changed) {
        this.broadcastPick();
      }
      result.connection?.close(1008, 'revoked');

      return Response.json({ ok: true, removed: result.removed });
    }

    if (request.headers.get('upgrade') !== 'websocket') {
      return new Response('websocket upgrade required', { status: 426 });
    }

    const authenticatedNodeId = request.headers.get(AUTHENTICATED_NODE_ID_HEADER)?.trim() ?? '';
    const nodeIdError = validateNodeId(authenticatedNodeId);
    if (nodeIdError) {
      return new Response('missing authenticated node_id', { status: 401 });
    }

    const pair = new WebSocketPair();
    const client = pair[0];
    const server = pair[1];
    server.accept();
    this.attach(server, authenticatedNodeId);

    return new Response(null, {
      status: 101,
      webSocket: client
    } as ResponseInit & { webSocket: WebSocket });
  }

  private async ensureLoaded(): Promise<void> {
    if (!this.loadPromise) {
      this.loadPromise = (async () => {
        const stored = await this.storage.get<StoredRoster>(ROSTER_KEY);
        this.state.hydrateRoster(stored ?? {});
        if (this.state.normalizeLoadedRoster()) {
          await this.persistIfNeeded(true);
        }
      })();
    }
    await this.loadPromise;
  }

  private async persistIfNeeded(changed: boolean): Promise<void> {
    if (!changed) {
      return;
    }
    await this.storage.put(ROSTER_KEY, this.state.exportRoster());
  }

  private broadcastPick(): void {
    const snapshot = this.state.currentPick();
    for (const connection of this.state.connectionsList()) {
      connection.send(JSON.stringify({
        type: 'pick',
        version: snapshot.version,
        upstream: snapshot.upstream
      } satisfies PickMessage));
    }
  }

  private sendPick(socket: WebSocket): void {
    const snapshot = this.state.currentPick();
    sendJson(socket, {
      type: 'pick',
      version: snapshot.version,
      upstream: snapshot.upstream
    } satisfies PickMessage);
  }

  private async flushReapResult(result: ReapResult): Promise<void> {
    await this.persistIfNeeded(result.rosterChanged);
    if (result.changed) {
      this.broadcastPick();
    }
    for (const connection of result.connectionsToClose) {
      connection.close(1001, 'stale');
    }
  }

  private attach(socket: WebSocket, authenticatedNodeId: string): void {
    let phase: SessionPhase = 'await_hello';
    const messageLimiter = new ConnectionRateLimiter();

    const helloTimer = setTimeout(() => {
      if (phase !== 'await_hello') {
        return;
      }
      sendJson(socket, {
        type: 'error',
        code: 'missing_hello',
        message: 'hello must be sent first'
      } satisfies ErrorMessage);
      phase = 'closed';
      socket.close(1008, 'missing_hello');
    }, HELLO_TIMEOUT_MS);

    let teardownTimer: ReturnType<typeof setTimeout> | null = null;
    let finalized = false;

    const clearTimers = (): void => {
      clearTimeout(helloTimer);
      if (teardownTimer !== null) {
        clearTimeout(teardownTimer);
        teardownTimer = null;
      }
    };

    const sendError = (code: string, message: string): void => {
      sendJson(socket, { type: 'error', code, message } satisfies ErrorMessage);
    };

    const finalize = async (): Promise<void> => {
      if (finalized) {
        return;
      }
      finalized = true;
      clearTimers();

      if (phase !== 'online') {
        phase = 'closed';
        return;
      }

      const result = this.state.abortConnection(authenticatedNodeId, socket);
      phase = 'closed';
      await this.persistIfNeeded(result.rosterChanged);
      if (result.changed) {
        this.broadcastPick();
      }
    };

    socket.addEventListener('message', event => {
      void this.handleMessage(
        event,
        authenticatedNodeId,
        socket,
        messageLimiter,
        sendError,
        () => phase,
        value => {
          phase = value;
        },
        () => {
          teardownTimer = setTimeout(() => {
            socket.close(1000, 'closing');
          }, TEARDOWN_CLOSE_TIMEOUT_MS);
        }
      );
    });

    socket.addEventListener('close', () => {
      void finalize();
    });
    socket.addEventListener('error', () => {
      void finalize();
    });
  }

  private async handleMessage(
    event: MessageEvent,
    authenticatedNodeId: string,
    socket: WebSocket,
    messageLimiter: ConnectionRateLimiter,
    sendError: (code: string, message: string) => void,
    getPhase: () => SessionPhase,
    setPhase: (phase: SessionPhase) => void,
    armTeardownClose: () => void
  ): Promise<void> {
    await this.flushReapResult(this.state.reapStaleNodes());

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

    const parsed = parseNodeInboundMessage(rawMessage);
    if (!parsed.message) {
      sendError('invalid_message', parsed.error ?? 'Invalid message');
      if (parsed.close) {
        socket.close(1008, 'invalid_message');
      }
      return;
    }
    const message = parsed.message;
    const phase = getPhase();

    if (message.type === 'hello') {
      if (phase !== 'await_hello') {
        sendError('invalid_message', 'hello may only be sent once');
        socket.close(1008, 'invalid_message');
        return;
      }

      const result = this.state.registerConnection(authenticatedNodeId, socket);
      if (result.throttled) {
        sendError('reconnect_throttled', 'node reconnect is temporarily throttled');
        socket.close(1013, 'reconnect_throttled');
        return;
      }

      await this.persistIfNeeded(result.rosterChanged);
      setPhase('online');
      sendJson(socket, {
        type: 'ready',
        node_id: authenticatedNodeId
      } satisfies ReadyMessage);

      if (result.changed) {
        this.broadcastPick();
      } else {
        this.sendPick(socket);
      }

      if (result.previous && result.previous !== socket) {
        result.previous.close(1012, 'replaced');
      }
      return;
    }

    if (phase === 'await_hello') {
      sendError('missing_hello', 'hello must be sent first');
      socket.close(1008, 'missing_hello');
      return;
    }

    if (phase !== 'online') {
      sendError('invalid_message', 'session is closing');
      socket.close(1008, 'invalid_message');
      return;
    }

    if (message.type === 'preferences') {
      const result = this.state.setPreferences(authenticatedNodeId, socket, message.upstreams, message.active_upstream ?? null);
      await this.persistIfNeeded(result.rosterChanged);
      if (result.changed) {
        this.broadcastPick();
      }
      return;
    }

    if (message.type === 'heartbeat') {
      const result = this.state.heartbeat(authenticatedNodeId, socket);
      await this.persistIfNeeded(result.rosterChanged);
      return;
    }

    if (message.type === 'bye') {
      const result = this.state.acceptTeardown(authenticatedNodeId, socket);
      await this.persistIfNeeded(result.rosterChanged);
      setPhase('teardown_accepted');
      if (result.changed) {
        this.broadcastPick();
      }
      sendJson(socket, {
        type: 'closing'
      } satisfies ClosingMessage);
      armTeardownClose();
    }
  }
}
