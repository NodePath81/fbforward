# Operations

This document covers routine operation of the local forwarder. Configuration
fields belong in [configuration](configuration.md); endpoint shapes belong in
[api](api.md).

## Start and stop

Run the binary with the validated configuration:

```bash
fbforward --config /etc/fbforward/config.yaml
```

For systemd, install the repository unit and keep the service restricted to
the capabilities it actually needs. `CAP_NET_ADMIN` and traffic-control
modules are not required.

On shutdown, listeners stop accepting new clients, active Flows close through
their normal lifecycle, audit queues drain, and the control server exits.

## Route overrides

Use `GetRouteStatus` to inspect effective upstreams. Use `SetRouteOverride` and
`ClearRouteOverride` for a single route. Static overrides are strict. Adaptive
overrides are soft preferences and fall back only within their configured
route. Existing Flows remain pinned.

## Health and measurement

Only adaptive-route upstreams are measured. The first probe is immediate;
successful probes schedule the next interval and failed probes use the retry
delay. TCP and UDP probes update one health state and RTT EWMA. `down` removes
an upstream from adaptive selection; `stale` is visible but does not mean the
Flow already moved.

If measurement is unavailable, verify the fbmeasure endpoint, firewall rules,
DNS resolution, and trusted-network access. Static routes can continue to
forward without fbmeasure.

## Control API and monitoring

The ControlServer listens on the configured HTTP address. Protect non-loopback
deployment with TLS termination or a trusted private network. The embedded
operator page is a thin polling client and stores its token only in the
current browser session.

Prometheus is available at `/metrics` when enabled. The compact metric set
covers active Flow counts and bounded Flow events, cumulative traffic by
upstream/protocol/direction, route selections, upstream health/RTT/probes,
Audit received/written/dropped records, firewall decisions, UDP rate-limit
drops, online-rule errors, and webhook results. Traffic rates should be
calculated with PromQL, for example:

```promql
rate(fbforward_traffic_bytes_total[1m]) * 8
```

Labels are limited to configured upstream/route names and fixed protocol,
direction, state, result, and rule-type values. Flow IDs, client addresses,
Flow Context tags, rule values, and error text are available through Audit or
logs rather than Prometheus labels.

## Audit and SQLite

Enable `ip_log` to persist complete Flow-close records and rejection events.
The `flows` table contains one row per complete TCP stream or UDP mapping;
active Flow identity is held separately in `flow_entities`. Queries use the
bounded Audit DSL and execute filtering, sorting, aggregation, and pagination
inside SQLite.

The operator page separates Flow Context from Audit. `CONTEXT` shows configured
backend identities, current unexpired Tags, and recent Tag actions. `FLOWS`
shows current effective Flow/Client Tags on active connections. `AUDIT` remains
the place for historical access records, Tag filtering, and traffic/Flow
aggregation such as `top tags`.

Use `QueryAudit` for routine searches. SQLite backup, restore, and integrity
helpers currently exist as internal Go library operations; they are not
exposed as a Control RPC or standalone CLI command. Perform offline
maintenance only when the process is stopped, using a small maintenance
program or an approved operational wrapper. Retention removes old Flow,
rejection, checkpoint, tag, policy, and online-rule event data according to
the configured intervals.

Flow Context tags are written transactionally and do not block forwarding.
After a Flow closes, tuple resolution and tagging remain available only during
the configured grace period. A trusted backend may also call `SetFlowLimit`,
`ClearFlowLimit`, or `BlockFlow` for an active Flow it is authorized to see.
The limit is bidirectional and can only tighten an existing policy. Blocking
closes that Flow with `backend_blocked`; use an online rule for future Flows.
The close transition is exactly-once; a repeated block or a request racing
with another close is rejected with `409`.

## Firewall policy

The persistent policy lives in its own strict YAML file. Deployment automation
should write a validated temporary file, atomically rename it into place, and
call `ReloadFirewallPolicy`. A failed reload leaves the current policy active;
listeners do not restart. Existing Flows keep their original admission
decision.

Online rules are separate TTL-bound runtime rules. They are not overwritten by
a persistent policy reload. Create, expire, and delete operations are audited.

## GeoIP updates

fbforward reads local MMDB files only. An external timer or configuration
management job downloads to a same-directory temporary file, validates the
database, atomically replaces the configured path, and calls `ReloadGeoIP`.
The service does not fetch GeoIP data itself.

## Webhook events

The optional `webhook` sink is asynchronous and bounded. It retries network
errors and HTTP 5xx responses at most twice after the initial attempt; HTTP 4xx
responses are not retried. Queue drops and final failures are exposed through
metrics and logs. It never blocks the forwarding path.

## Troubleshooting checklist

1. Run the configuration check and inspect the first startup error.
2. Confirm listener bind addresses are unused and protocol-correct.
3. Confirm the selected upstream destination and listener port are reachable.
4. For adaptive routes, test fbmeasure TCP and UDP endpoints from the
   fbforward host.
5. Check `GetStatus`, `GetRouteStatus`, and Prometheus health metrics.
6. Check firewall policy and rejection audit records.
7. Check SQLite, audit dropped-record metrics, and webhook error metrics for
   local resource failures.

An upstream failure does not migrate an existing Flow. Test a new connection
when verifying fallback or a route override change.
