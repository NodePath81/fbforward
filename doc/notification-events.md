# Notification event reference

This document describes the current outbound notification events emitted by
`fbforward` toward `fbnotify`.

Events are emitted only when notification delivery is configured for the
service. They are generated from runtime state transitions, not from log
parsing. The names listed here are the canonical `event_name` values sent to
`fbnotify`.

Current event set:

- `fbforward`: 2 events

---

## fbforward

`fbforward` emits events with `source.service = "fbforward"` and
`source.instance` taken from `notify.source_instance` or the resolved host
name.

### `upstream.unusable`

Severity:

- `warn`

Trigger:

- emitted per upstream, not as a global outage event
- the same upstream must remain continuously unusable for at least
  `notify.unusable_interval`
- the process must also have been up for at least
  `notify.startup_grace_period`
- the first notification fires only after both timing conditions are satisfied

Repeat behavior:

- while the upstream remains unusable, reminder notifications may repeat
- reminders are rate-limited to at most once per `notify.notify_interval`
- when the upstream becomes usable again, its pending timer and repeat
  suppression state are cleared
- a later unusable period starts a new alert episode

Attributes:

- `upstream.tag`
- `upstream.reason`

Notes:

- there is no recovery event for `upstream.unusable`
- defaults:
  - `notify.startup_grace_period = 5m`
  - `notify.unusable_interval = 30s`
  - `notify.notify_interval = 30m`

Example payload:

```json
{
  "schema_version": 1,
  "event_name": "upstream.unusable",
  "severity": "warn",
  "timestamp": "2026-04-10T12:00:30Z",
  "source": {
    "service": "fbforward",
    "instance": "node-1"
  },
  "attributes": {
    "upstream.tag": "us-1",
    "upstream.reason": "failover_loss"
  }
}
```

Example config:

```yaml
notify:
  enabled: true
  endpoint: http://10.99.0.30:8787/v1/events
  key_id: notify-key
  token: replace-with-fbnotify-token
  source_instance: node-1
  startup_grace_period: 5m
  unusable_interval: 30s
  notify_interval: 30m
```

### `system.test_notification`

Severity:

- `info`

Trigger:

- explicit manual operator action through the control API

Attributes:

- `test.origin = "manual"`
- `test.service = "fbforward"`

Notes:

- this is a test-only event used to validate notification wiring
- it does not indicate an operational fault
- success means the event entered the existing `fbforward` notification queue

Example payload:

```json
{
  "schema_version": 1,
  "event_name": "system.test_notification",
  "severity": "info",
  "timestamp": "2026-04-10T12:06:00Z",
  "source": {
    "service": "fbforward",
    "instance": "node-1"
  },
  "attributes": {
    "test.origin": "manual",
    "test.service": "fbforward"
  }
}
```
