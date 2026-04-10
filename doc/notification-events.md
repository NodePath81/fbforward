# Notification event reference

This document describes the current outbound notification events emitted by
`fbforward` and `fbcoord` toward `fbnotify`.

These events are emitted only when notification delivery is configured for the
service. They are not derived by parsing logs. The event names listed here are
the canonical `event_name` values sent to `fbnotify`.

Current event set:

- `fbforward`: 3 events
- `fbcoord`: 3 events

---

## fbforward

`fbforward` emits events with `source.service = "fbforward"` and
`source.instance` taken from `notify.source_instance` or the resolved host
name.

### `upstream.active_cleared`

Severity:

- `critical` when the sustained outage starts
- `info` when the outage resolves

Trigger:

- `activeTag` becomes empty and stays empty for at least 30 seconds
- the process must have been up for at least 5 minutes before the outage alert
  can fire
- the recovery notification is emitted on the first transition back to any
  usable upstream after the alert has fired

Attributes:

- `notification.state = "active"` on alert onset
- `notification.state = "resolved"` on recovery

Notes:

- the same event name is reused for onset and recovery
- transient empty-active periods shorter than 30 seconds do not emit a
  notification

### `upstream.active_changed`

Severity:

- `warn`

Trigger:

- emitted only when the upstream switch reason is one of:
  - `failover_loss`
  - `failover_retrans`
  - `failover_dial`

It is not emitted for routine score-driven or warmup-driven switches.

Attributes:

- `switch.from`
- `switch.to`
- `switch.reason`

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

---

## fbcoord

`fbcoord` emits events with `source.service = "fbcoord"` and
`source.instance = FBNOTIFY_SOURCE_INSTANCE`.

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

### `operator.login`

Severity:

- `info`

Trigger:

- successful `POST /api/auth/login` after operator-token validation and session
  creation

Attributes:

- `client.ip` from the raw `cf-connecting-ip` header when present
- `client.country` from `request.cf.country` when present
- `client.city` from `request.cf.city` when present
- `client.region` from `request.cf.region` when present

Notes:

- failed login attempts do not emit this event
- the emitted IP is the raw client IP header value, not a normalized
  rate-limit key

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
