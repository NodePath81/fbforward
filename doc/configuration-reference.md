# Configuration reference

The YAML schema is strict. Unknown and removed keys fail startup. The active
configuration is intentionally small; host-level traffic shaping is configured
outside fbforward with tc/systemd/Ansible.

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
    measurement: {port: 9876}
```

`static` routes use their configured/default upstream and never fail over.
`adaptive` routes use health, RTT, priority, and configuration order within
their own upstream set. A route override affects new Flows only.

## Runtime sections

- `forwarding.limits`: maximum TCP connections and UDP mappings.
- `forwarding.idle_timeout`: TCP/UDP idle durations.
- `upstreams`: unique tags, destination hosts, measurement endpoint, priority.
- `dns`: optional resolver list and address strategy.
- `measurement`: fbmeasure schedule, security, and TCP/UDP probe settings.
- `health`: RTT EWMA alpha, failure/recovery thresholds, stale threshold.
- `control`: HTTP bind address/port, bearer token, Prometheus toggle.
- `webhook`: optional endpoint, bearer token, instance, and timeout.
- `geoip`: local `asn_db_path` and `country_db_path` only.
- `ip_log`: SQLite audit path, queues, batching, retention, and pruning.
- `flow_context`: remote backend identities, scopes, and TTL.
- `firewall`: persistent policy file and initial-load behavior.

## Removed features

The `shaping`, `notify`, GeoIP URL/refresh fields, and standalone bandwidth
probe configuration are rejected. Use `webhook` for generic events, external
deployment automation for GeoIP downloads, and fbmeasure for health/RTT.

GeoIP updates should download to a temporary file, validate the MMDB, atomically
replace the configured local path, then call `ReloadGeoIP`.
