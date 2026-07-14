# Architecture

fbforward is a Linux TCP/UDP forwarder with a small authenticated control
plane. It chooses one upstream for each new Flow and keeps that choice until
the Flow ends. The repository builds two binaries: `fbforward` and the
`fbmeasure` probe server.

## Runtime components

```text
client
  -> TCP/UDP listener
  -> admission and online policy
  -> route-local selector
  -> pinned upstream
  -> audit / metrics observers

fbmeasure probes -> unified health and RTT snapshot -> adaptive selector
Control HTTP -> RPC, Flow Context, polling status, Prometheus
```

The data plane forwards bytes without application parsing. TCP uses one stream
per accepted connection. UDP uses one mapping per client tuple and ends it
after idle timeout or shutdown.

The control plane exposes authenticated JSON-RPC and HTTP endpoints. The
embedded page is a dependency-free polling client; it is not a separate
runtime or protocol. Prometheus is read-only. GeoIP databases are local files
and are replaced by deployment automation before `ReloadGeoIP` is called.

## Configuration topology

Listeners, routes, and upstreams form an explicit graph:

```text
listener -> route -> upstream list
```

`static` routes use their configured default upstream. They may have an
operator override, but do not automatically fail over. `adaptive` routes
select only from their own upstream list using health, RTT, priority, and
configuration order. An adaptive override is a soft preference: if it is
unavailable, new Flows use route-local fallback; recovery restores the
override preference. Existing Flows are never migrated.

## Flow lifecycle

- A firewall or connection-limit rejection creates a `Rejection`, not a Flow.
- After admission and upstream selection, a Flow receives a cryptographically
  random FlowID and immutable metadata: protocol, client, listener, route,
  upstream, and start time.
- Updates contain cumulative byte counters and last activity.
- TCP closes when the stream ends; UDP closes on mapping idle timeout or
  shutdown. Close is idempotent and emits at most one summary.
- Persistent policy, online rules, health changes, DNS changes, and route
  overrides affect new Flows only.

The Flow Context Registry also stores the backend socket tuple while a Flow is
active and for a short grace period after close. A backend can resolve that
tuple through the authenticated HTTP API and attach Flow or client tags.

## Health and selection

Only upstreams used by adaptive routes are measured. The first probe is due
immediately. TCP and UDP probe observations update one `HealthSnapshot`:

```text
state: unknown | healthy | down | stale
rtt: successful-probe EWMA
last_success_at / last_attempt_at
consecutive_successes / consecutive_failures
```

Any successful probe in a cycle counts as a successful cycle. Failure and
recovery thresholds control `down` and `healthy`; `stale` is derived at read
time from the last successful probe. Dial failures use a separate short
cooldown and do not alter RTT health.

Adaptive candidate ordering is:

1. remove down and dial-cooldown upstreams;
2. prefer healthy, then stale, then unknown;
3. prefer lower measured RTT;
4. prefer higher configured priority;
5. preserve configuration order.

Static routes do not create a health scheduler. Manual route overrides are
route-local; the deprecated single-route `SetUpstream` wrapper does not define
new data-plane selection semantics.

## Policy and audit

Admission follows this order:

```text
hard limits -> online deny -> persistent firewall -> online actions -> allow
```

Persistent policy is a strict versioned YAML file compiled into an immutable
snapshot and atomically replaced on reload. Online rules are TTL-bound SQLite
records loaded into a separate immutable snapshot. A policy reload never
overwrites online rules.

SQLite is the authoritative local audit store. `flows` contains one complete
lifecycle row per TCP stream or UDP mapping. `flow_entities` contains active
identity and backend tuple data; it is not a partial Flow summary. Checkpoints
are coalesced cumulative snapshots, not packet records. Flow tags, client tags,
policy events, rejection events, and online-rule events are transactional.

Queries apply filtering, ordering, aggregation, and pagination in SQLite. IP
binary columns support CIDR filtering without loading the full table into Go.

## Shutdown and boundaries

Runtime shutdown closes listeners, stops measurement, drains audit queues, and
stops the control plane. Existing Flow close behavior remains explicit and
observable.

The service does not implement transparent socket identity propagation, kernel
traffic control, distributed state, arbitrary SQL, application-layer proxying,
or cross-route upstream selection.
