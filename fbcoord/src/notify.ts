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

export interface Notifier {
  enabled(): boolean;
  send(eventName: string, severity: NotifySeverity, attributes?: Record<string, unknown>): Promise<void>;
}

function trim(value: string | undefined): string {
  return value?.trim() ?? '';
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

export function createNotifier(env: NotifyEnv, sourceService: string, fetchImpl: typeof fetch = fetch): Notifier {
  const url = trim(env.FBNOTIFY_URL);
  const keyId = trim(env.FBNOTIFY_KEY_ID);
  const token = trim(env.FBNOTIFY_TOKEN);
  const sourceInstance = trim(env.FBNOTIFY_SOURCE_INSTANCE);
  const active = url !== '' && keyId !== '' && token !== '' && sourceInstance !== '';

  return {
    enabled(): boolean {
      return active;
    },

    async send(eventName: string, severity: NotifySeverity, attributes: Record<string, unknown> = {}): Promise<void> {
      if (!active) {
        return;
      }

      const event: NotificationEvent = {
        schema_version: 1,
        event_name: eventName,
        severity,
        timestamp: new Date().toISOString(),
        source: {
          service: sourceService,
          instance: sourceInstance
        },
        attributes
      };

      const rawBody = JSON.stringify(event);
      const headerTimestamp = String(Math.floor(Date.now() / 1000));

      try {
        const signature = await sign(token, `${headerTimestamp}.${rawBody}`);
        await fetchImpl(url, {
          method: 'POST',
          headers: {
            'content-type': 'application/json',
            'x-fbnotify-key-id': keyId,
            'x-fbnotify-timestamp': headerTimestamp,
            'x-fbnotify-signature': signature
          },
          body: rawBody
        });
      } catch {
        // Best-effort notification delivery must never affect caller behavior.
      }
    }
  };
}
