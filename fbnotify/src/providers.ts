import type { DeliveryResult, NotificationEvent, ProviderTargetRecord } from './types';

function eventTitle(event: NotificationEvent): string {
  return `${event.source.service}:${event.event_name}`;
}

function eventText(event: NotificationEvent): string {
  const lines = [
    `${event.severity.toUpperCase()} ${event.event_name}`,
    `service=${event.source.service}`,
    `instance=${event.source.instance}`,
    `timestamp=${String(event.timestamp)}`
  ];
  const keys = Object.keys(event.attributes).sort();
  for (const key of keys) {
    const value = event.attributes[key];
    lines.push(`${key}=${typeof value === 'string' ? value : JSON.stringify(value)}`);
  }
  return lines.join('\n');
}

function severityPriority(severity: NotificationEvent['severity']): string {
  switch (severity) {
    case 'critical':
      return '1';
    default:
      return '0';
  }
}

export async function deliverToTarget(
  target: ProviderTargetRecord,
  event: NotificationEvent,
  captureStub: DurableObjectStub,
  fetchImpl: typeof fetch = fetch
): Promise<DeliveryResult> {
  try {
    if (target.type === 'capture') {
      const payload = eventText(event);
      const response = await captureStub.fetch(new Request('https://capture.internal/record', {
        method: 'POST',
        headers: {
          'content-type': 'application/json'
        },
        body: JSON.stringify({
          target_id: target.id,
          target_name: target.name,
          target_type: target.type,
          event_name: event.event_name,
          severity: event.severity,
          source_service: event.source.service,
          source_instance: event.source.instance,
          payload
        })
      }));
      return {
        ok: response.ok,
        status: response.status,
        target_id: target.id,
        target_name: target.name,
        target_type: target.type,
        ...(response.ok ? {} : { error: 'capture delivery failed' })
      };
    }

    if (target.type === 'webhook') {
      const response = await fetchImpl(target.config.type === 'webhook' ? target.config.url : 'https://invalid.local/', {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          'x-fbnotify-event-name': event.event_name,
          'x-fbnotify-severity': event.severity,
          'x-fbnotify-source-service': event.source.service
        },
        body: JSON.stringify(event)
      });
      return {
        ok: response.ok,
        status: response.status,
        target_id: target.id,
        target_name: target.name,
        target_type: target.type,
        ...(response.ok ? {} : { error: `webhook responded with ${response.status}` })
      };
    }

    const pushoverConfig = target.config.type === 'pushover' ? target.config : null;
    const payload = new URLSearchParams({
      token: pushoverConfig?.api_token ?? '',
      user: pushoverConfig?.user_key ?? '',
      title: eventTitle(event),
      message: eventText(event),
      priority: severityPriority(event.severity),
      ...(pushoverConfig?.device ? { device: pushoverConfig.device } : {})
    });
    const response = await fetchImpl('https://api.pushover.net/1/messages.json', {
      method: 'POST',
      headers: {
        'content-type': 'application/x-www-form-urlencoded;charset=UTF-8'
      },
      body: payload.toString()
    });
    return {
      ok: response.ok,
      status: response.status,
      target_id: target.id,
      target_name: target.name,
      target_type: target.type,
      ...(response.ok ? {} : { error: `pushover responded with ${response.status}` })
    };
  } catch (error) {
    return {
      ok: false,
      status: null,
      target_id: target.id,
      target_name: target.name,
      target_type: target.type,
      error: error instanceof Error ? error.message : 'delivery failed'
    };
  }
}
