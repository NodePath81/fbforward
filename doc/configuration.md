# Configuration

The YAML schema is strict. Unknown and removed fields fail startup. The
complete sample is [configs/config.example.yaml](/home/huangyj/Workspace/fbforward/configs/config.example.yaml);
this document explains the stable sections without duplicating that file.

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
it can be referenced. `static` routes use one default upstream and never
automatically fail over. `adaptive` routes require at least two upstreams and
select only from their own list.

## Runtime sections

- `forwarding.limits`: TCP connection and UDP mapping caps.
- `forwarding.idle_timeout`: TCP and UDP inactivity limits.
- `upstreams`: destination host, unique tag, optional measurement endpoint and
  priority.
- `dns`: optional resolver addresses and IPv4/IPv6 strategy.
- `measurement`: adaptive-route probe schedule, protocol settings, and
  fbmeasure transport security.
- `health`: RTT EWMA alpha, failure/recovery thresholds, and stale threshold.
- `control`: HTTP bind address/port, bearer token, and Prometheus toggle.
- `webhook`: optional asynchronous generic event endpoint.
- `geoip`: local ASN and country MMDB paths only.
- `ip_log`: SQLite path, queues, batching, retention, and pruning.
- `flow_context`: backend identities, route/upstream scopes, namespaces, and
  maximum tag TTL.
- `firewall`: external policy file and initial-load failure behavior.

## Route behavior

Static route selection ignores health and RTT. An operator override may select
another configured upstream, but an unavailable static target fails new Flows.
Adaptive selection filters down/cooldown targets, prefers healthy and lower RTT
upstreams, then priority and configuration order. An unavailable adaptive
override falls back within the same route and recovers automatically.

All selections affect new Flows only.

## Removed configuration

Legacy traffic-control, distributed-mode, throughput-probe, and ranking fields,
the old embedded listener topology, inline legacy policy when `policy_file` is
set, remote GeoIP download settings, and the old event section are rejected.
Use host automation for traffic control, `webhook` for events, and deployment
automation for GeoIP downloads followed by `ReloadGeoIP`.
