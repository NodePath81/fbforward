# Logging Guidelines

This document defines mandatory logging principles for `fbforward` and `bwprobe` code changes.

## 1. Scope and goals

Use logs to support:
- operational debugging and reliability
- incident response and audit investigation
- correlation across control-plane requests, data-plane flows, and measurement cycles

This is a structured-logging standard. Do not add free-form operational logs.

## 2. Structured logging rules

1. Use structured attributes only. Do not encode important context in free text.
2. For `fbforward` operational events, use `util.Event(...)` so `event.name` is always present.
3. Use component loggers (`util.ComponentLogger(...)`) so `component` is always present.
4. Do not introduce ad-hoc key aliases. Reuse canonical keys from the requirements document.
5. Log one event per meaningful state transition (entry, decision, completion, failure), not per packet/hot-loop iteration unless explicitly rate-limited and justified.

## 3. Required correlation fields

Include correlation IDs whenever the context exists:
- `request.id` for HTTP/RPC/identity/metrics request flows
- `ws.conn_id` for WebSocket session lifecycle/events
- `flow.id` for TCP/UDP data-plane mapping lifecycle
- `measure.cycle_id` for measurement run lifecycle

If a correlation field is not available at a pre-open/pre-allocation step, emit the event without it and ensure the subsequent lifecycle event includes it.

## 4. Event naming convention

Use canonical, stable event names:
- lowercase
- dot-separated namespace
- `<component>.<event>` format

Examples:
- `control.rpc.request_completed`
- `forward.tcp.connection_opened`
- `upstream.active_changed`

Do not invent near-duplicates of existing canonical events.

## 5. OTel alignment

JSON logs must align with the OpenTelemetry-inspired structure used in this repo:
- top-level keys: `ts`, `severity`, `severity_number`, `body`, `attributes`, `resource`
- event/context fields must be in `attributes`
- service metadata in `resource` (`service.name`, `service.version`)

Severity guidance:
- `ERROR`: unrecoverable operation failure
- `WARN`: degraded or rejected operation
- `INFO`: lifecycle milestones and normal boundaries
- `DEBUG`: diagnostic detail

## 6. Privacy and redaction rules

Never log secrets or credential material:
- auth tokens (`Authorization`, subprotocol token payloads)
- passwords, API keys, private keys, session secrets
- raw request/response bodies containing user or sensitive payloads

Allowed and expected for security audit paths:
- network endpoints (`client.ip`, `client.addr`, `upstream`, `upstream.ip`, `upstream.addr`)
- request metadata (`request.method`, `request.path`, `http.user_agent`)
- auth outcome and policy decisions (`auth.*`, `access.policy.*`)

When sensitive values are needed for debugging, log a redacted form or a derived indicator (for example boolean, enum, hash prefix) rather than raw value.

## 7. Required audit coverage (where applicable)

### 7.1 Control plane

For authenticated or protected endpoints, ensure logs capture:
- caller network identity (`client.ip`, `client.addr`)
- request metadata (`request.method`, `request.path`, user-agent)
- auth result (`auth.method`, `auth.authenticated`, `auth.identity` sentinel when needed)
- policy decision (`access.policy.name`, `access.policy.decision`, `access.policy.reason`)
- completion outcome (`http.status_code`, `result`, `latency_ms`, error on failure)

### 7.2 Data plane mapping lifecycle

For flow open/close events, ensure logs capture:
- mapping identity (`flow.id`)
- client to upstream relationship (`client.ip` -> `upstream`/`upstream.ip`/`upstream.addr`)
- lifecycle outcome (`result`, `flow.close_reason`, durations/bytes on close)

## 8. Examples

### 8.1 Preferred pattern in code

```go
logger := util.ComponentLogger(baseLogger, util.CompControl)
util.Event(logger, slog.LevelInfo, "control.rpc.request_completed",
    "request.id", reqID,
    "request.method", r.Method,
    "request.path", r.URL.Path,
    "client.ip", clientIP,
    "http.status_code", status,
    "latency_ms", latencyMs,
    "result", result,
)
```

### 8.2 Anti-patterns

```go
// Bad: unstructured and missing required correlation/fields
logger.Info("rpc failed for user request")

// Bad: leaks credential material
logger.Info("auth header", "authorization", r.Header.Get("Authorization"))
```

## 9. Acceptance checks for code review

For any change that adds/modifies logging:

1. No unstructured operational logs were introduced.
2. `event.name` and `component` are present on emitted operational events.
3. Required correlation fields are present for the event context (`request.id`, `ws.conn_id`, `flow.id`, `measure.cycle_id` where applicable).
4. Security/audit-sensitive paths include access outcome and policy/auth decision coverage where applicable.
5. No secrets or raw sensitive payloads are logged.

Recommended grep-based checks:

```bash
# Spot direct logger level calls in fbforward operational code.
rg 'logger\.(Info|Warn|Error|Debug)\(' internal cmd

# Spot potentially sensitive keys accidentally logged.
rg -n 'authorization|auth_token|password|secret|api[_-]?key|private[_-]?key' internal cmd
```
