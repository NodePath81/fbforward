# fbforward configuration reference

This document describes the YAML configuration schema for `fbforward`.

## Units

- Bandwidth (bits/sec): `k`, `m`, `g` suffixes (SI). Examples: `500k`, `10m`, `1g`.
- Data size (bytes): `kb`, `mb` suffixes (SI) or bare bytes. Examples: `1200`, `500kb`, `1mb`.
- Duration: Go duration strings (e.g., `500ms`, `15s`, `5m`) or bare numbers (seconds).

## Example configuration

```yaml
hostname: fbforward-01

forwarding:
  listeners:
    - bind_addr: 0.0.0.0
      bind_port: 9000
      protocol: tcp
      shaping:
        upload_limit: 50m
        download_limit: 200m
    - bind_addr: 0.0.0.0
      bind_port: 9000
      protocol: udp
  limits:
    max_tcp_connections: 50
    max_udp_mappings: 500
  idle_timeout:
    tcp: 60s
    udp: 30s

upstreams:
  - tag: primary
    destination:
      host: 203.0.113.10
    measurement:
      port: 9876
    priority: 0
    bias: 0

reachability:
  probe_interval: 1s
  window_size: 5

measurement:
  startup_delay: 10s
  stale_threshold: 60m
  fallback_to_icmp_on_stale: true
  schedule:
    interval:
      min: 15m
      max: 45m
    upstream_gap: 5s
    headroom:
      max_link_utilization: 0.7
      required_free_bandwidth: "0"
  protocols:
    tcp:
      enabled: true
      alternate: true
      target_bandwidth:
        upload: 10m
        download: 50m
      chunk_size: 1200
      sample_size: 500kb
      sample_count: 1
      timeout:
        per_sample: 10s
        per_cycle: 30s
    udp:
      enabled: true
      target_bandwidth:
        upload: 10m
        download: 50m
      chunk_size: 1200
      sample_size: 500kb
      sample_count: 1
      timeout:
        per_sample: 10s
        per_cycle: 30s

scoring:
  smoothing:
    alpha: 0.2
  reference:
    tcp:
      bandwidth:
        upload: 10m
        download: 50m
      latency:
        rtt: 50
        jitter: 10
      retransmit_rate: 0.01
    udp:
      bandwidth:
        upload: 10m
        download: 50m
      latency:
        rtt: 50
        jitter: 10
      loss_rate: 0.01
  weights:
    tcp:
      bandwidth_upload: 0.15
      bandwidth_download: 0.25
      rtt: 0.25
      jitter: 0.10
      retransmit_rate: 0.25
    udp:
      bandwidth_upload: 0.10
      bandwidth_download: 0.30
      rtt: 0.15
      jitter: 0.30
      loss_rate: 0.15
    protocol_blend:
      tcp_weight: 0.5
      udp_weight: 0.5
  utilization_penalty:
    enabled: true
    window_duration: 5s
    update_interval: 1s
    threshold: 0.7
    min_multiplier: 0.3
    exponent: 2
  bias_transform:
    kappa: 0.693

switching:
  auto:
    confirm_duration: 15s
    score_delta_threshold: 5
    min_hold_time: 30s
  failover:
    loss_rate_threshold: 0.2
    retransmit_rate_threshold: 0.2
  close_flows_on_failover: false

control:
  bind_addr: 127.0.0.1
  bind_port: 8080
  auth_token: "change-me"
  webui:
    enabled: true
  metrics:
    enabled: true

shaping:
  enabled: false
  interface: eth0
  ifb_device: ifb0
  aggregate_limit: 1g
```

## Top-level

- `hostname` (string, optional): override hostname for identity endpoints.
- `forwarding` (object, required): listener definitions and flow limits.
- `upstreams` (list, required): upstream destinations.
- `dns` (object, optional): custom DNS settings.
- `reachability` (object, optional): ICMP reachability probing.
- `measurement` (object, optional): bwprobe-based measurement scheduling.
- `scoring` (object, optional): scoring parameters and weights.
- `switching` (object, optional): auto switching and failover behavior.
- `control` (object, required): control plane bind + auth.
- `shaping` (object, optional): Linux `tc` shaping configuration.

## forwarding

- `listeners` (list, required): listener definitions.
  - `bind_addr` (string, required): bind address.
  - `bind_port` (int, required): listener port (forwarded 1:1).
  - `protocol` (string, required): `tcp` or `udp`.
  - `shaping` (object, optional): per-listener shaping (requires `shaping.enabled`).
    - `upload_limit` (string): client upload cap.
    - `download_limit` (string): client download cap.
- `limits` (object, optional): flow caps.
  - `max_tcp_connections` (int, default: `50`).
  - `max_udp_mappings` (int, default: `500`).
- `idle_timeout` (object, optional): flow idle timeouts.
  - `tcp` (duration, default: `60s`).
  - `udp` (duration, default: `30s`).

## upstreams

Each entry:

- `tag` (string, required): stable identifier used in RPC/UI/metrics.
- `destination.host` (string, required): IP literal or hostname.
- `measurement.host` (string, optional): measurement server host (defaults to destination host).
- `measurement.port` (int, optional, default: `9876`).
- `priority` (float, optional): fast-start priority bonus (default: `0`).
- `bias` (float, optional): user bias in `[-1,1]` (default: `0`).
- `shaping` (object, optional): per-upstream shaping (requires `shaping.enabled`).
  - `upload_limit` (string): fbforward -> upstream cap.
  - `download_limit` (string): upstream -> fbforward cap.

Notes:
- Upstream shaping applies to all resolved IPs of a hostname.

## dns

- `servers` (list, optional): custom DNS servers (`ip` or `ip:port`).
- `strategy` (string, optional): `ipv4_only` or `prefer_ipv6`.

## reachability

- `probe_interval` (duration, default: `1s`): ICMP probe interval.
- `window_size` (int, default: `5`): probes per reachability window.
- `startup_delay` (duration, default: `window_size * probe_interval`).

## measurement

- `startup_delay` (duration, default: `10s`): delay before first measurement.
- `stale_threshold` (duration, default: `60m`): metric staleness threshold.
- `fallback_to_icmp_on_stale` (bool, default: `true`).
- `schedule.interval.min` (duration, default: `15m`).
- `schedule.interval.max` (duration, default: `45m`).
- `schedule.upstream_gap` (duration, default: `5s`).
- `schedule.headroom.max_link_utilization` (float, default: `0.7`): current-load threshold; if current utilization exceeds this value, measurements are skipped. If utilization is below the threshold, the measurement still must fit in remaining capacity.
- `schedule.headroom.required_free_bandwidth` (string, default: `"0"`): extra headroom required beyond target bandwidth (example: target 100m + 10m headroom requires 110m remaining).
- `fast_start.enabled` (bool, default: `true`).
- `fast_start.timeout` (duration, default: `500ms`).
- `fast_start.warmup_duration` (duration, default: `15s`).

Protocol settings (`measurement.protocols.tcp` / `measurement.protocols.udp`):

- `enabled` (bool, default: `true`).
- `alternate` (bool, TCP only, default: `true`).
- `target_bandwidth.upload` (string, default: `10m`).
- `target_bandwidth.download` (string, default: `50m`).
- `chunk_size` (string, default: `1200`).
- `sample_size` (string, default: `500kb`).
- `sample_count` (int, default: `1`).
- `timeout.per_sample` (duration, default: `10s`).
- `timeout.per_cycle` (duration, default: `30s`).

## scoring

- `smoothing.alpha` (float, default: `0.2`): EMA smoothing factor.
- `reference.tcp.bandwidth.upload` / `download` (string).
- `reference.tcp.latency.rtt` / `jitter` (ms).
- `reference.tcp.retransmit_rate` (float, 0..1].
- `reference.udp.bandwidth.upload` / `download` (string).
- `reference.udp.latency.rtt` / `jitter` (ms).
- `reference.udp.loss_rate` (float, 0..1].
- `weights.tcp` and `weights.udp`: per-metric weights (normalized if needed).
- `weights.protocol_blend.tcp_weight` / `udp_weight` (sum to 1).
- `utilization_penalty.enabled` (bool, default: `true`).
- `utilization_penalty.window_duration` (duration, default: `5s`).
- `utilization_penalty.update_interval` (duration, default: `1s`).
- `utilization_penalty.threshold` (float, default: `0.7`).
- `utilization_penalty.min_multiplier` (float, default: `0.3`).
- `utilization_penalty.exponent` (float, default: `2`).
- `bias_transform.kappa` (float, default: `0.693`).

## switching

- `auto.confirm_duration` (duration, default: `15s`).
- `auto.score_delta_threshold` (float, default: `5`).
- `auto.min_hold_time` (duration, default: `30s`).
- `failover.loss_rate_threshold` (float, default: `0.2`).
- `failover.retransmit_rate_threshold` (float, default: `0.2`).
- `close_flows_on_failover` (bool, default: `false`).

## control

- `bind_addr` (string, default: `127.0.0.1`).
- `bind_port` (int, default: `8080`).
- `auth_token` (string, required): bearer token for `/rpc` and `/status`.
- `webui.enabled` (bool, default: `true`).
- `metrics.enabled` (bool, default: `true`).

## shaping

- `enabled` (bool, default: `false`).
- `interface` (string, required when enabled): network device to shape.
- `ifb_device` (string, default: `ifb0`).
- `aggregate_limit` (string, default: `1g`).

Notes:
- Shaping uses HTB + fq_codel and requires `CAP_NET_ADMIN`.
- Listener shaping uses per-port rules; upstream shaping uses per-IP rules.
- Enabling shaping resets root/ingress qdiscs on the device and IFB.
