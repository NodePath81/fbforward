import type { CaptureMessage } from '../types';

const CAPTURE_MESSAGES_KEY = 'messages';
const MAX_CAPTURE_MESSAGES = 200;

interface RecordCaptureBody {
  target_id?: string;
  target_name?: string;
  target_type?: CaptureMessage['target_type'];
  event_name?: string;
  severity?: CaptureMessage['severity'];
  source_service?: string;
  source_instance?: string;
  payload?: string;
}

function json(data: unknown, status: number = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: {
      'content-type': 'application/json; charset=utf-8'
    }
  });
}

function randomId(): string {
  const bytes = new Uint8Array(12);
  crypto.getRandomValues(bytes);
  return btoa(String.fromCharCode(...bytes)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '');
}

export class CaptureStore {
  constructor(
    private readonly storage: DurableObjectStorage,
    private readonly now: () => number = () => Date.now()
  ) {}

  async list(): Promise<CaptureMessage[]> {
    return ((await this.storage.get<CaptureMessage[]>(CAPTURE_MESSAGES_KEY)) ?? [])
      .slice()
      .sort((left: CaptureMessage, right: CaptureMessage) => right.received_at - left.received_at);
  }

  async clear(): Promise<void> {
    await this.storage.put(CAPTURE_MESSAGES_KEY, []);
  }

  async record(body: RecordCaptureBody): Promise<CaptureMessage> {
    if (!body.target_id || !body.target_name || !body.target_type || !body.event_name || !body.severity || !body.source_service || !body.source_instance || typeof body.payload !== 'string') {
      throw new Error('invalid capture payload');
    }
    const messages = await this.list();
    const message: CaptureMessage = {
      id: randomId(),
      target_id: body.target_id,
      target_name: body.target_name,
      target_type: body.target_type,
      event_name: body.event_name,
      severity: body.severity,
      source_service: body.source_service,
      source_instance: body.source_instance,
      received_at: this.now(),
      payload: body.payload
    };
    messages.unshift(message);
    await this.storage.put(CAPTURE_MESSAGES_KEY, messages.slice(0, MAX_CAPTURE_MESSAGES));
    return message;
  }
}

export class CaptureDurableObject {
  private readonly store: CaptureStore;

  constructor(state: DurableObjectState) {
    this.store = new CaptureStore(state.storage);
  }

  async fetch(request: Request): Promise<Response> {
    const url = new URL(request.url);

    if (request.method === 'GET' && url.pathname === '/messages') {
      return json({ messages: await this.store.list() });
    }

    if (request.method === 'POST' && url.pathname === '/record') {
      try {
        return json(await this.store.record(await request.json() as RecordCaptureBody));
      } catch (error) {
        return json({ error: error instanceof Error ? error.message : 'invalid capture request' }, 400);
      }
    }

    if (request.method === 'POST' && url.pathname === '/clear') {
      await this.store.clear();
      return json({ ok: true });
    }

    return json({ error: 'not found' }, 404);
  }
}
