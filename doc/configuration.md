# Configuration

The YAML schema is strict for the active configuration shape. Unknown or
removed fields fail startup. The complete sample is
[configs/config.example.yaml](../configs/config.example.yaml); this document
explains the stable sections without duplicating that file.

## Topology

```yaml
listeners:
  - name: web
    bind: 0.0.0.0:443
    protocol: tcp
    route: web
routes:
  - name: web
    strategy: adaptive
    upstreams: [primary, backup]
upstreams:
  - tag: primary
    destination: {host: 203.0.113.10}
```

Listener names and bind/protocol pairs are unique. A route must exist before
it can be referenced. `bind` accepts IPv4, IPv6, and `:port` forms; ports are
in the range 1–65535 and protocols are `tcp` or `udp`.

`static` routes must contain at least one upstream. With one upstream,
`default_upstream` is filled from that item; with multiple upstreams,
`default_upstream` is required and must belong to the route. Static routes
never automatically fail over. `adaptive` routes require at least two
upstreams, select only from their own list, and must not set
`default_upstream`.

The previous `forwarding.listeners` shape is still accepted during the
compatibility period. It is normalized into top-level listeners and routes and
adds a deprecation warning to the loaded configuration. New files should use
the explicit topology above. Listener names, route names, upstream tags, and
route membership must remain unique.

## Runtime sections

- `forwarding.limits`: TCP connection and UDP mapping caps.
- `forwarding.idle_timeout`: TCP and UDP inactivity limits.
- `upstreams`: destination host, unique tag, optional measurement endpoint and
  priority.
- `dns`: optional resolver addresses and IPv4/IPv6 strategy.
- `measurement`: adaptive-route probe schedule, a bounded `probe_timeout`, and
  TCP/UDP protocol enable switches. Only upstreams referenced by adaptive
  routes require scheduled probes. fbmeasure is a fixed small-packet echo
  service; network access is controlled outside fbforward.
- `health`: RTT EWMA alpha in `(0,1]`, positive failure/recovery thresholds,
  and a positive stale threshold.
- `control`: HTTP bind address/port, bearer token (at least 16 characters),
  and the Prometheus toggle.
- `webhook`: optional asynchronous generic event endpoint. When enabled,
  `endpoint` must be HTTP(S); the optional bearer token and source instance
  identify the outbound event sender.
- `logging`: `level` is `debug`, `info`, `warn`, or `error`; `format` is
  `text` or `json`.
- `geoip`: local ASN and country MMDB paths only. Database downloads are an
  external deployment task.
- `ip_log`: SQLite path, queue sizes, batching, retention, flush, and prune
  intervals. `db_path` and positive queue/batch/flush values are required when
  enabled.
- `flow_context`: backend identities, route/upstream scopes, namespaces, and
  maximum tag TTL. Enabling it requires `ip_log.enabled` and at least one
  identity; backend tokens must differ from the control token.
- `firewall`: external policy file and initial-load failure behavior. With
  `fail_on_initial_load: true`, an unreadable or invalid initial policy stops
  startup; otherwise a degraded deny-all policy is installed. The policy file
  and legacy inline rules cannot be configured together.

Measurement schedule intervals must be positive, with `max >= min`; the
upstream gap may be zero. At least one measurement protocol must be enabled.
`measurement.probe_timeout` must be between `100ms` and `10s`. The probe
sample count and frame size are fixed by fbmeasure and cannot be configured.
The removed `security`, `ping_count`, `per_sample`, and `per_cycle` fields are
rejected by strict decoding.

## Route behavior

Static route selection ignores health and RTT. An operator override may select
another configured upstream, but an unavailable static target fails new Flows.
Adaptive selection filters down/cooldown targets, prefers healthy and lower RTT
upstreams, then priority and configuration order. An unavailable adaptive
override falls back within the same route and recovers automatically.

All selections affect new Flows only.

## Forwarding and upstream details

`forwarding.limits.max_tcp_connections` limits accepted TCP connections and
`max_udp_mappings` limits active client mappings. Both values must be positive.
`forwarding.idle_timeout.tcp` and `.udp` close inactive resources; they do not
change an already selected route or upstream.

Each upstream has a unique `tag` and a destination host. The listener port is
used when constructing the destination endpoint, so a listener on port 443
connects to port 443 at the selected upstream address. An optional upstream
measurement host/port controls where fbmeasure probes; if omitted, the
destination host and the default probe port are used. `priority` is consulted
only when adaptive candidates otherwise tie.

DNS servers are optional. An empty server list uses the system resolver;
`ipv4_only` restricts address resolution, while `prefer_ipv6` changes address
preference without disabling IPv4 fallback. DNS refresh does not move an
existing Flow.

The control listener should remain on loopback unless a deployment provides
TLS termination or a trusted private network. The control token is never
returned by `GetRuntimeConfig`. Flow Context identities use separate tokens
and can be restricted independently by route, upstream, and namespace.

When `geoip.enabled` is true, at least one local MMDB path is required. A
database may be updated by an external timer using an atomic same-filesystem
replacement, followed by `ReloadGeoIP`; the service itself performs no
network download.

## Removed configuration

Legacy traffic-control, distributed-mode, throughput-probe, and ranking fields,
remote GeoIP download settings, and the old event section are rejected. The
legacy embedded listener topology is accepted only as a migration path and
emits a warning. Legacy inline firewall policy is also accepted when no
`policy_file` is set, but emits a warning; configuring both sources is an
error. Use host automation for traffic control, `webhook` for events, and
deployment automation for GeoIP downloads followed by `ReloadGeoIP`.

`ip_log` is also the persistence prerequisite for online runtime rules. If
SQLite audit storage is disabled, online-rule and Flow Context operations are
unavailable rather than silently using an in-memory substitute.
