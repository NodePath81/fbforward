import type { NotifyConfigInfo, NotifyConfigSource } from './durable-objects/token';

const encoder = new TextEncoder();

export interface NotifyEnv {
  FBNOTIFY_URL?: string;
  FBNOTIFY_KEY_ID?: string;
  FBNOTIFY_TOKEN?: string;
  FBNOTIFY_SOURCE_INSTANCE?: string;
}

export type NotifySeverity = 'info' | 'warn' | 'critical';

interface NotificationEvent {
  schema_version: number;
  event_name: string;
  severity: NotifySeverity;
  timestamp: string;
  source: {
    service: string;
    instance: string;
  };
  attributes: Record<string, unknown>;
}

export interface ResolvedNotifyConfig extends NotifyConfigInfo {
  token?: string;
}

export interface Notifier {
  status(): Promise<NotifyConfigInfo>;
  send(eventName: string, severity: NotifySeverity, attributes?: Record<string, unknown>): Promise<void>;
}

function logNotification(
  level: 'info' | 'warn',
  action: 'attempt' | 'delivered' | 'delivery_failed',
  details: Record<string, unknown>
): void {
  const payload = {
    component: 'notify',
    service: 'fbcoord',
    action,
    ...details
  };
  if (level === 'warn') {
    console.warn('fbcoord notification', payload);
    return;
  }
  console.info('fbcoord notification', payload);
}

function trim(value: string | undefined): string {
  return value?.trim() ?? '';
}

export function resolveEnvNotifyConfig(env: NotifyEnv): ResolvedNotifyConfig {
  const endpoint = trim(env.FBNOTIFY_URL);
  const keyId = trim(env.FBNOTIFY_KEY_ID);
  const token = trim(env.FBNOTIFY_TOKEN);
  const sourceInstance = trim(env.FBNOTIFY_SOURCE_INSTANCE);
  const missing: string[] = [];
  if (endpoint === '') {
    missing.push('endpoint');
  }
  if (keyId === '') {
    missing.push('key_id');
  }
  if (token === '') {
    missing.push('token');
  }
  if (sourceInstance === '') {
    missing.push('source_instance');
  }

  return {
    configured: missing.length === 0,
    source: missing.length === 0 ? 'bootstrap-env' : 'none',
    endpoint,
    key_id: keyId,
    source_instance: sourceInstance,
    masked_prefix: token ? `${token.slice(0, 8)}...` : '',
    updated_at: null,
    missing,
    token,
  };
}

function summarizeNotifyConfig(config: ResolvedNotifyConfig): NotifyConfigInfo {
  return {
    configured: config.configured,
    source: config.source,
    endpoint: config.endpoint,
    key_id: config.key_id,
    source_instance: config.source_instance,
    masked_prefix: config.masked_prefix,
    updated_at: config.updated_at,
    missing: [...config.missing],
  };
}

function trimResponseBody(value: string): string {
  const normalized = value.trim();
  if (normalized.length <= 256) {
    return normalized;
  }
  return `${normalized.slice(0, 256)}...`;
}

function bytesToBase64Url(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '');
}

async function sign(secret: string, payload: string): Promise<string> {
  const key = await crypto.subtle.importKey(
    'raw',
    encoder.encode(secret),
    { name: 'HMAC', hash: 'SHA-256' },
    false,
    ['sign']
  );
  const signature = await crypto.subtle.sign('HMAC', key, encoder.encode(payload));
  return bytesToBase64Url(new Uint8Array(signature));
}

export function createNotifier(
  env: NotifyEnv,
  sourceService: string,
  fetchImpl: typeof fetch = fetch,
  resolveConfig: (() => Promise<ResolvedNotifyConfig>) | null = null
): Notifier {
  const loadConfig = resolveConfig ?? (async () => resolveEnvNotifyConfig(env));
  return {
    async status(): Promise<NotifyConfigInfo> {
      return summarizeNotifyConfig(await loadConfig());
    },

    async send(eventName: string, severity: NotifySeverity, attributes: Record<string, unknown> = {}): Promise<void> {
      const config = await loadConfig();
      if (!config.configured || !config.token) {
        logNotification('warn', 'delivery_failed', {
          event_name: eventName,
          severity,
          source: config.source,
          missing: config.missing,
        });
        return;
      }

      const event: NotificationEvent = {
        schema_version: 1,
        event_name: eventName,
        severity,
        timestamp: new Date().toISOString(),
        source: {
          service: sourceService,
          instance: config.source_instance
        },
        attributes
      };

      const rawBody = JSON.stringify(event);
      const headerTimestamp = String(Math.floor(Date.now() / 1000));

      try {
        logNotification('info', 'attempt', {
          event_name: eventName,
          severity,
          endpoint: config.endpoint,
          key_id: config.key_id,
          source: config.source as NotifyConfigSource,
          source_instance: config.source_instance,
          attribute_keys: Object.keys(attributes).sort(),
        });
        const signature = await sign(config.token, `${headerTimestamp}.${rawBody}`);
        const response = await fetchImpl(config.endpoint, {
          method: 'POST',
          headers: {
            'content-type': 'application/json',
            'x-fbnotify-key-id': config.key_id,
            'x-fbnotify-timestamp': headerTimestamp,
            'x-fbnotify-signature': signature
          },
          body: rawBody
        });
        if (response.ok) {
          logNotification('info', 'delivered', {
            event_name: eventName,
            severity,
            endpoint: config.endpoint,
            key_id: config.key_id,
            source: config.source as NotifyConfigSource,
            source_instance: config.source_instance,
            http_status: response.status
          });
          return;
        }
        const responseBody = trimResponseBody(await response.text());
        logNotification('warn', 'delivery_failed', {
          event_name: eventName,
          severity,
          endpoint: config.endpoint,
          key_id: config.key_id,
          source: config.source as NotifyConfigSource,
          source_instance: config.source_instance,
          http_status: response.status,
          response_body: responseBody
        });
      } catch (error) {
        const message = error instanceof Error ? error.message : String(error);
        logNotification('warn', 'delivery_failed', {
          event_name: eventName,
          severity,
          endpoint: config.endpoint,
          key_id: config.key_id,
          source: config.source as NotifyConfigSource,
          source_instance: config.source_instance,
          error: message
        });
        // Best-effort notification delivery must never affect caller behavior.
      }
    }
  };
}
