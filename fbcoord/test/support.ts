export class MemoryStorage implements DurableObjectStorage {
  private readonly values = new Map<string, unknown>();
  private alarmAt: number | null = null;

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

  async getAlarm(): Promise<number | null> {
    return this.alarmAt;
  }

  async setAlarm(scheduledTime: number | Date): Promise<void> {
    this.alarmAt = scheduledTime instanceof Date ? scheduledTime.getTime() : scheduledTime;
  }

  async deleteAlarm(): Promise<void> {
    this.alarmAt = null;
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

export class FactoryNamespace implements DurableObjectNamespace {
  private readonly stubs = new Map<string, DurableObjectStub>();

  constructor(private readonly factory: (name: string) => DurableObjectStub) {}

  idFromName(name: string): DurableObjectId {
    return { __name: name } as DurableObjectId;
  }

  get(id: DurableObjectId): DurableObjectStub {
    const name = (id as { __name?: string }).__name;
    if (!name) {
      throw new Error('missing durable object name');
    }
    let stub = this.stubs.get(name);
    if (!stub) {
      stub = this.factory(name);
      this.stubs.set(name, stub);
    }
    return stub;
  }
}

interface StoredKVValue {
  value: string;
  expiresAt: number | null;
}

export class MemoryKV implements KVNamespace {
  private readonly values = new Map<string, StoredKVValue>();

  constructor(private readonly now: () => number = () => Date.now()) {}

  async get<T>(key: string, type?: 'json'): Promise<string | T | null> {
    const entry = this.values.get(key);
    if (!entry) {
      return null;
    }
    if (entry.expiresAt !== null && entry.expiresAt <= this.now()) {
      this.values.delete(key);
      return null;
    }
    if (type === 'json') {
      return JSON.parse(entry.value) as T;
    }
    return entry.value;
  }

  async put(key: string, value: string, options?: { expirationTtl?: number }): Promise<void> {
    this.values.set(key, {
      value,
      expiresAt: options?.expirationTtl ? this.now() + (options.expirationTtl * 1000) : null
    });
  }

  async delete(key: string): Promise<void> {
    this.values.delete(key);
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

export function createExecutionContext(): {
  ctx: ExecutionContext;
  flush(): Promise<void>;
} {
  const pending: Promise<unknown>[] = [];
  return {
    ctx: {
      waitUntil(promise: Promise<unknown>): void {
        pending.push(promise);
      }
    },
    async flush(): Promise<void> {
      await Promise.all(pending.splice(0));
    }
  };
}

export function createDurableObjectState(storage: DurableObjectStorage = new MemoryStorage()): {
  state: DurableObjectState;
  flush(): Promise<void>;
} {
  const pending: Promise<unknown>[] = [];
  return {
    state: {
      storage,
      waitUntil(promise: Promise<unknown>): void {
        pending.push(promise);
      }
    },
    async flush(): Promise<void> {
      await Promise.all(pending.splice(0));
    }
  };
}
