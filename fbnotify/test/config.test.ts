import { describe, expect, it } from 'vitest';

import { ConfigStore, resolveRouteTargets } from '../src/durable-objects/config';
import type { NotificationEvent } from '../src/types';
import { MemoryStorage } from './support';

describe('ConfigStore', () => {
  it('resolves routes by precedence and supports fan-out', async () => {
    let now = 1_000;
    const store = new ConfigStore(new MemoryStorage(), () => now);

    const globalTarget = await store.createTarget({
      name: 'global-capture',
      type: 'capture',
      config: {}
    });
    const eventTarget = await store.createTarget({
      name: 'webhook-a',
      type: 'webhook',
      config: {
        url: 'https://hooks.example.com/a'
      }
    });
    const serviceEventTarget = await store.createTarget({
      name: 'webhook-b',
      type: 'webhook',
      config: {
        url: 'https://hooks.example.com/b'
      }
    });

    await store.createRoute({
      name: 'global',
      target_ids: [globalTarget.id]
    });
    await store.createRoute({
      name: 'event',
      event_name: 'upstream.active_changed',
      target_ids: [eventTarget.id]
    });
    await store.createRoute({
      name: 'service+event',
      source_service: 'fbforward',
      event_name: 'upstream.active_changed',
      target_ids: [eventTarget.id, serviceEventTarget.id]
    });

    const event: NotificationEvent = {
      schema_version: 1,
      event_name: 'upstream.active_changed',
      severity: 'warn',
      timestamp: now,
      source: {
        service: 'fbforward',
        instance: 'node-1'
      },
      attributes: {}
    };

    const targets = await store.resolveTargetsForEvent(event);
    expect(targets.map(target => target.id)).toEqual([eventTarget.id, serviceEventTarget.id]);

    const fallbackTargets = await store.resolveTargetsForEvent({
      ...event,
      event_name: 'something.else'
    });
    expect(fallbackTargets.map(target => target.id)).toEqual([globalTarget.id]);
  });

  it('rejects duplicate route scopes', async () => {
    const store = new ConfigStore(new MemoryStorage(), () => 2_000);
    const target = await store.createTarget({
      name: 'capture',
      type: 'capture',
      config: {}
    });

    await store.createRoute({
      name: 'global',
      target_ids: [target.id]
    });

    await expect(store.createRoute({
      name: 'global-duplicate',
      target_ids: [target.id]
    })).rejects.toThrow('same match scope');
  });
});

describe('resolveRouteTargets', () => {
  it('returns an empty list when no route matches', () => {
    expect(resolveRouteTargets({
      schema_version: 1,
      event_name: 'missing.route',
      severity: 'info',
      timestamp: 1,
      source: { service: 'svc', instance: 'inst' },
      attributes: {}
    }, [], [])).toEqual([]);
  });
});
