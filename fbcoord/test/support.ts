export class MemoryStorage implements DurableObjectStorage {
  private readonly values = new Map<string, unknown>();

  async get<T>(key: string): Promise<T | undefined> {
    const value = this.values.get(key);
    return value === undefined ? undefined : structuredClone(value) as T;
  }

  async put<T>(key: string, value: T): Promise<void> {
    this.values.set(key, structuredClone(value));
  }

  async delete(key: string): Promise<boolean> {
    return this.values.delete(key);
  }
}

export class RecordingStub implements DurableObjectStub {
  readonly requests: Request[] = [];

  constructor(
    private readonly handler: (request: Request) => Promise<Response> | Response = () =>
      new Response('ok', { status: 200 })
  ) {}

  async fetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
    const request = input instanceof Request ? input : new Request(input, init);
    this.requests.push(request.clone());
    return this.handler(request);
  }
}

export class StaticNamespace implements DurableObjectNamespace {
  private readonly stubs = new Map<string, DurableObjectStub>();

  constructor(entries: Record<string, DurableObjectStub> = {}) {
    for (const [name, stub] of Object.entries(entries)) {
      this.stubs.set(name, stub);
    }
  }

  set(name: string, stub: DurableObjectStub): void {
    this.stubs.set(name, stub);
  }

  idFromName(name: string): DurableObjectId {
    return { __name: name } as DurableObjectId;
  }

  get(id: DurableObjectId): DurableObjectStub {
    const name = (id as { __name?: string }).__name;
    const stub = name ? this.stubs.get(name) : undefined;
    if (!stub) {
      throw new Error(`missing durable object stub for ${name ?? 'unknown'}`);
    }
    return stub;
  }
}

export function jsonResponse(data: unknown, status: number = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: {
      'content-type': 'application/json; charset=utf-8'
    }
  });
}
