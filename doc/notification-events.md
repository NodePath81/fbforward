# Notification event reference

This document describes the current outbound notification events emitted by
`fbforward` and `fbcoord` toward `fbnotify`.

Events are emitted only when notification delivery is configured for the
service. They are generated from runtime state transitions, not from log
parsing. The names listed here are the canonical `event_name` values sent to
`fbnotify`.

Current event set:

- `fbforward`: 3 events
- `fbcoord`: 3 events

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

### `coordination.session_ended`

Severity:

- `warn`

Trigger:

- a previously connected coordination session disconnects and stays disconnected
  for at least 30 seconds

Attributes:

- `coordination.endpoint`

Notes:

- this event is emitted only after a real connected session has been observed
- there is no recovery notification for reconnect in the current implementation

Example payload:

```json
{
  "schema_version": 1,
  "event_name": "coordination.session_ended",
  "severity": "warn",
  "timestamp": "2026-04-10T12:05:30Z",
  "source": {
    "service": "fbforward",
    "instance": "node-1"
  },
  "attributes": {
    "coordination.endpoint": "https://fbcoord.example"
  }
}
```

### `system.test_notification`

Severity:

- `info`

Trigger:

- explicit manual operator action from the `fbforward` web UI

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

---

## fbcoord

`fbcoord` emits events with `source.service = "fbcoord"` and
`source.instance` taken from its effective `fbnotify` sender configuration.

### `pool.node_aborted`

Severity:

- `warn`

Trigger:

- a node transitions to `aborted` in the pool roster from one of these paths:
  - heartbeat timeout
  - unexpected disconnect
  - Durable Object load normalization of persisted `online` state

Graceful teardown to `offline` does not emit this event.

Attributes:

- `pool.name = "global"`
- `node.id`
- `cause`

Current `cause` values:

- `timeout`
- `disconnect`
- `load-normalization`

Example payload:

```json
{
  "schema_version": 1,
  "event_name": "pool.node_aborted",
  "severity": "warn",
  "timestamp": "2026-04-10T12:00:00Z",
  "source": {
    "service": "fbcoord",
    "instance": "fbcoord"
  },
  "attributes": {
    "pool.name": "global",
    "node.id": "node-1",
    "cause": "timeout"
  }
}
```

### `operator.token_rotated`

Severity:

- `warn`

Trigger:

- successful `POST /api/token/rotate`

Attributes:

- none in the current implementation

Notes:

- this reflects operator-token rotation only
- it does not revoke existing operator sessions
- it does not rotate or revoke node tokens

Example payload:

```json
{
  "schema_version": 1,
  "event_name": "operator.token_rotated",
  "severity": "warn",
  "timestamp": "2026-04-10T12:15:00Z",
  "source": {
    "service": "fbcoord",
    "instance": "fbcoord"
  },
  "attributes": {}
}
```

### `system.test_notification`

Severity:

- `info`

Trigger:

- explicit manual operator action from the `fbcoord` token page

Attributes:

- `test.origin = "manual"`
- `test.service = "fbcoord"`

Notes:

- this is a test-only event used to validate notification wiring
- it does not indicate an operational fault
- success means the event entered the existing `fbcoord` notification send path

Example payload:

```json
{
  "schema_version": 1,
  "event_name": "system.test_notification",
  "severity": "info",
  "timestamp": "2026-04-10T12:20:00Z",
  "source": {
    "service": "fbcoord",
    "instance": "fbcoord"
  },
  "attributes": {
    "test.origin": "manual",
    "test.service": "fbcoord"
  }
}
```
