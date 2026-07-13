# Health and route-selection specification

This document describes the active fbforward selection model. Retired scoring,
ICMP, fast-start, and protocol-loss designs are summarized in
[`doc/archive/phase10-pre-health.md`](archive/phase10-pre-health.md).

## Health observations

Adaptive-route upstreams are measured by the fbmeasure TCP and UDP ping
operations. Each completed probe produces one observation:

```text
success, RTT, observed_at
```

Both protocols update the same `HealthSnapshot`:

```text
state
rtt EWMA
last_success_at
last_attempt_at
consecutive_successes
consecutive_failures
```

Successful observations update the RTT EWMA and reset failures. Failed
observations increment failures. `failure_threshold` moves an upstream to
`down`; `recovery_threshold` successful observations move it back to
`healthy`. A successful state becomes `stale` when it exceeds
`health.stale_threshold` without a new success.

## Route-local selection

Static routes select `default_upstream` directly. They may list additional
configured upstreams for operator overrides, but health state is not consulted
and an unavailable selected upstream does not trigger automatic fallback.

Adaptive routes select only from their configured upstream list:

1. Exclude `down` and dial-cooldown candidates.
2. Prefer `healthy`, then `stale`, then `unknown`.
3. Prefer lower RTT when both candidates have a measured RTT.
4. Prefer higher configured priority.
5. Preserve configuration order as the final tie-breaker.

An adaptive route may have a route-local override. A usable override is chosen
first; when it is down or cooling, the selector retains the override but falls
back to the route's adaptive candidates. Recovery restores the override as the
new-flow preference. A route never selects an upstream outside its own list.

## Flow pinning

Selection occurs once when a TCP stream or UDP mapping is created. Health
updates, DNS changes, policy reloads, and later route selections do not migrate
an existing Flow.

## Measurement scheduling

The first probe for every adaptive upstream/protocol is due immediately. After
a successful probe, the next due time is selected within the configured
`measurement.schedule.interval` range. Failed probes are retried after the
fixed retry delay. `upstream_gap` limits the start time between jobs.

Static-only configurations do not create a measurement scheduler or connect to
fbmeasure.
