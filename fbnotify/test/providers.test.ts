import { describe, expect, it } from 'vitest';

import { deliverToTarget } from '../src/providers';
import type { NotificationEvent, ProviderTargetRecord } from '../src/types';
import { RecordingStub } from './support';

const EVENT: NotificationEvent = {
  schema_version: 1,
  event_name: 'demo.alert',
  severity: 'critical',
  timestamp: '2026-04-09T00:00:00Z',
  source: {
    service: 'fbforward',
    instance: 'node-1'
  },
  attributes: {
    reason: 'test'
  }
};

describe('deliverToTarget', () => {
  it('sends webhook payloads as JSON', async () => {
    const requests: Request[] = [];
    const target: ProviderTargetRecord = {
      id: 't1',
      name: 'webhook',
      type: 'webhook',
      config: {
        type: 'webhook',
        url: 'https://hooks.example.com/notify'
      },
      created_at: 1,
      updated_at: 1
    };

    const result = await deliverToTarget(target, EVENT, new RecordingStub(), async (input, init) => {
      requests.push(new Request(input, init));
      return new Response('ok', { status: 200 });
    });

    expect(result.ok).toBe(true);
    expect(requests).toHaveLength(1);
    await expect(requests[0]!.json()).resolves.toEqual(EVENT);
  });

  it('formats pushover payloads as form data', async () => {
    const target: ProviderTargetRecord = {
      id: 't2',
      name: 'pushover',
      type: 'pushover',
      config: {
        type: 'pushover',
        api_token: 'app-token-abcdefghijklmnopqrstuvwxyz12',
        user_key: 'user-key-abcdefghijklmnopqrstuvwxyz34',
        device: 'iphone'
      },
      created_at: 1,
      updated_at: 1
    };

    const requests: Request[] = [];
    const result = await deliverToTarget(target, EVENT, new RecordingStub(), async (input, init) => {
      requests.push(new Request(input, init));
      return new Response('ok', { status: 200 });
    });

    expect(result.ok).toBe(true);
    expect(requests).toHaveLength(1);
    expect(requests[0]!.url).toBe('https://api.pushover.net/1/messages.json');
    const payload = await requests[0]!.text();
    expect(payload).toContain('token=app-token-abcdefghijklmnopqrstuvwxyz12');
    expect(payload).toContain('user=user-key-abcdefghijklmnopqrstuvwxyz34');
    expect(payload).toContain('device=iphone');
  });

  it('stores capture deliveries through the capture durable object', async () => {
    const capture = new RecordingStub();
    const target: ProviderTargetRecord = {
      id: 't3',
      name: 'capture',
      type: 'capture',
      config: {
        type: 'capture'
      },
      created_at: 1,
      updated_at: 1
    };

    const result = await deliverToTarget(target, EVENT, capture);

    expect(result.ok).toBe(true);
    expect(capture.requests).toHaveLength(1);
    await expect(capture.requests[0]!.json()).resolves.toMatchObject({
      target_id: 't3',
      event_name: 'demo.alert',
      source_service: 'fbforward'
    });
  });
});
