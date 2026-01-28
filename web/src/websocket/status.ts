import type { WSMessage } from '../types';

interface StatusSocketOptions {
  token: string;
  onMessage: (msg: WSMessage) => void;
  onOpen?: () => void;
  onClose?: () => void;
}

export function connectStatusSocket(options: StatusSocketOptions, snapshotIntervalMs: number = 10000) {
  let ws: WebSocket | null = null;
  let reconnectTimer: number | null = null;
  let attempts = 0;
  let closed = false;
  let currentSnapshotIntervalMs = snapshotIntervalMs;

  const connect = () => {
    if (closed) {
      return;
    }
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const protocols = ['fbforward', `fbforward-token.${base64UrlEncode(options.token)}`];
    ws = new WebSocket(`${proto}://${location.host}/status`, protocols);

    ws.addEventListener('open', () => {
      attempts = 0;
      ws?.send(JSON.stringify({ type: 'subscribe', interval_ms: currentSnapshotIntervalMs }));
      options.onOpen?.();
    });

    ws.addEventListener('message', event => {
      try {
        const msg = JSON.parse(event.data) as WSMessage;
        options.onMessage(msg);
      } catch (err) {
        // ignore malformed messages
      }
    });

    ws.addEventListener('close', () => {
      options.onClose?.();
      if (!closed) {
        scheduleReconnect();
      }
    });

    ws.addEventListener('error', () => {
      ws?.close();
    });
  };

  const scheduleReconnect = () => {
    attempts += 1;
    const delay = Math.min(10000, 1000 + attempts * 800);
    if (reconnectTimer) {
      window.clearTimeout(reconnectTimer);
    }
    reconnectTimer = window.setTimeout(connect, delay);
  };

  connect();

  return {
    close() {
      closed = true;
      if (reconnectTimer) {
        window.clearTimeout(reconnectTimer);
      }
      ws?.close();
    },
    updateSnapshotInterval(newIntervalMs: number) {
      if (!Number.isFinite(newIntervalMs) || newIntervalMs <= 0) {
        return;
      }
      currentSnapshotIntervalMs = newIntervalMs;
      if (ws?.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'subscribe', interval_ms: currentSnapshotIntervalMs }));
      }
    }
  };
}

function base64UrlEncode(text: string): string {
  const encoder = new TextEncoder();
  const bytes = encoder.encode(text);
  let binary = '';
  bytes.forEach(value => {
    binary += String.fromCharCode(value);
  });
  return btoa(binary)
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/g, '');
}
