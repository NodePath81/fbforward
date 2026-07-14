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
DNS resolution, and `measurement.security`. Static routes can continue to
forward without fbmeasure.

## Control API and monitoring

The ControlServer listens on the configured HTTP address. Protect non-loopback
deployment with TLS termination or a trusted private network. The embedded
operator page is a thin polling client and stores its token only in the
current browser session.

Prometheus is available at `/metrics` when enabled. Useful views include
active Flow counts, upstream health/RTT, probe counters, route selections,
firewall decisions, audit queue depth, and webhook results.

## Audit and SQLite

Enable `ip_log` to persist complete Flow-close records and rejection events.
The `flows` table contains one row per complete TCP stream or UDP mapping;
active Flow identity is held separately in `flow_entities`. Queries use the
bounded Audit DSL and execute filtering, sorting, aggregation, and pagination
inside SQLite.

Use `QueryAudit` for routine searches. Use SQLite backup/restore helpers for
offline maintenance. Retention removes old Flow, rejection, checkpoint, tag,
policy, and online-rule event data according to the configured intervals.

Flow Context tags are written transactionally and do not block forwarding.
After a Flow closes, tuple resolution and tagging remain available only during
the configured grace period.

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
7. Check SQLite and webhook queue/error metrics for local resource failures.

An upstream failure does not migrate an existing Flow. Test a new connection
when verifying fallback or a route override change.
