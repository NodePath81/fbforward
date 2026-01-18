# fbforward configuration reference

This document describes every YAML field supported by `fbforward`, its purpose, and allowed values.

## Example configuration

```yaml
listeners:
  - addr: 0.0.0.0
    port: 9000
    protocol: tcp
    egress:
      rate: 50m
    ingress:
      rate: 200m
  - addr: 0.0.0.0
    port: 9000
    protocol: udp
upstreams:
  - tag: primary
    host: 203.0.113.10
    measure_host: 203.0.113.10
    measure_port: 9876
    priority: 0
    bias: 0
    egress:
      rate: 100m
    ingress:
      rate: 500m
  - tag: backup
    host: example.net
resolver:
  servers:
    - 1.1.1.1
probe:
  interval: 1s
  window_size: 5
  discovery_delay: 5s
measurement:
  interval: 2s
  discovery_delay: 10s
  schedule:
    min_interval: 15m
    max_interval: 45m
    inter_upstream_gap: 5s
    max_utilization: 0.7
    required_headroom: "0"
  tcp_target_bandwidth_up: 10m
  tcp_target_bandwidth_down: 50m
  udp_target_bandwidth_up: 10m
  udp_target_bandwidth_down: 50m
  sample_bytes: 500KB
  samples: 1
  tcp_enabled: true
  udp_enabled: true
  alternate_tcp: true
  max_sample_duration: 10s
  max_cycle_duration: 30s
  fast_start_timeout: 500ms
  warmup_duration: 15s
  stale_threshold: 120s
  fallback_to_icmp: true
scoring:
  ema_alpha: 0.2
  utilization_window_sec: 5
  utilization_update_sec: 1
  ref_bandwidth_up: 10m
  ref_bandwidth_down: 50m
  ref_rtt_ms: 50
  ref_jitter_ms: 10
  ref_retrans_rate: 0.01
  ref_loss_rate: 0.01
  weights_tcp:
    bandwidth_up: 0.15
    bandwidth_down: 0.25
    rtt: 0.25
    jitter: 0.10
    retrans: 0.25
  weights_udp:
    bandwidth_up: 0.10
    bandwidth_down: 0.30
    rtt: 0.15
    jitter: 0.30
    loss: 0.15
  protocol_weight_tcp: 0.5
  protocol_weight_udp: 0.5
  utilization_enabled: true
  utilization_min_mult: 0.3
  utilization_threshold: 0.7
  utilization_exponent: 2
  bias_kappa: 0.693
switching:
  confirm_duration: 15s
  failure_loss_threshold: 0.2
  failure_retrans_threshold: 0.2
  switch_threshold: 5
  min_hold_seconds: 30
  close_flows_on_unusable: false
limits:
  max_tcp_conns: 50
  max_udp_mappings: 500
timeouts:
  tcp_idle_seconds: 60
  udp_idle_seconds: 30
control:
  addr: 127.0.0.1
  port: 8080
  token: "change-me"
webui:
  enabled: true
shaping:
  enabled: false
  device: eth0
  ifb: ifb0
  aggregate_bandwidth: 1g
```

## Top-level

- `listeners` (list, required): listener definitions.
- `upstreams` (list, required): global upstream list.
- `resolver` (object, optional): custom DNS settings.
- `probe` (object, optional): ICMP reachability probe scheduling.
- `measurement` (object, optional): bwprobe measurement scheduling + timeouts.
- `scoring` (object, optional): scoring/EMA parameters.
- `switching` (object, optional): auto switching behavior.
- `limits` (object, optional): connection/mapping caps.
- `timeouts` (object, optional): idle timeouts in seconds.
- `control` (object, required): control plane bind + token.
- `webui` (object, optional): SPA enable/disable.
- `shaping` (object, optional): traffic shaping via Linux `tc`.

## listeners

Each entry:

- `addr` (string, required): bind address (IPv4/IPv6 literal or hostname).
- `port` (int, required): listener port, forwarded to upstream host on same port.
- `protocol` (string, required): `tcp` or `udp`.
- `ingress` (object, optional): per-listener download shaping.
- `egress` (object, optional): per-listener upload shaping.

Notes:
- `ingress`/`egress` require `shaping.enabled: true` and a valid `shaping.device`.

## upstreams

Each entry:

- `tag` (string, required): stable identifier used in RPC/UI/metrics.
- `host` (string, required): IP literal or hostname.
- `measure_host` (string, optional): host/IP of the measurement server (defaults to `host`).
- `measure_port` (int, optional): measurement server port (default: `9876`).
- `priority` (float, optional): static priority bonus for fast start (default: `0`).
- `bias` (float, optional): user bias in `[-1,1]` (default: `0`).
- `ingress` (object, optional): per-upstream download shaping (traffic FROM upstream).
- `egress` (object, optional): per-upstream upload shaping (traffic TO upstream).

Notes:
- `ingress`/`egress` require `shaping.enabled: true` and a valid `shaping.device`.
- For hostname upstreams, shaping is applied to all resolved IPs.
- Upstream shaping uses IP-based flower filters (lower priority than port-based listener shaping).

## resolver

- `servers` (list of string, optional): custom DNS servers. Each entry can be `ip` or `ip:port`.
  - If `ip` is provided, port `53` is used.
  - If omitted or empty, system DNS is used.
- `strategy` (string, optional): address selection strategy.
  - `ipv4_only`: use only IPv4 (A) results; ignore AAAA records.
  - `prefer_ipv6`: use IPv6 (AAAA) results when present; fall back to IPv4 (A) when no IPv6 records exist.
  - If omitted, all resolved addresses are used in resolver order.

## probe (reachability only)

- `interval` (duration, default: `1s`): ICMP probe interval per upstream.
- `window_size` (int, default: `5`): number of probes per reachability window.
- `discovery_delay` (duration, default: `window_size * interval`): wait before reachability-only initialization.

Duration format:
- String: Go duration (e.g., `500ms`, `2s`, `1m`).
- Number: seconds (int/float).

## measurement

- `interval` (duration, default: `2s`): legacy measurement cycle interval (deprecated; use `schedule.min_interval`).
- `discovery_delay` (duration, default: `10s`): delay before starting measurements.
- `schedule` (object): randomized measurement scheduling.
  - `min_interval` (duration, default: `15m`): minimum interval between measurements.
  - `max_interval` (duration, default: `45m`): maximum interval between measurements.
  - `inter_upstream_gap` (duration, default: `5s`): gap between measurements across upstreams.
  - `max_utilization` (float, default: `0.7`): utilization cap before deferring measurements.
  - `required_headroom` (string, default: `"0"`): minimum headroom in bps; `"0"` uses target bandwidth.
- `tcp_target_bandwidth_up` (string, default: `10m`): target TCP uplink bandwidth.
- `tcp_target_bandwidth_down` (string, default: `50m`): target TCP downlink bandwidth.
- `udp_target_bandwidth_up` (string, default: `10m`): target UDP uplink bandwidth.
- `udp_target_bandwidth_down` (string, default: `50m`): target UDP downlink bandwidth.
- `sample_bytes` (string, default: `500KB`): payload bytes per sample.
- `samples` (int, default: `1`): samples per direction per cycle.
- `tcp_enabled` (bool, default: `true`): enable TCP measurements.
- `udp_enabled` (bool, default: `true`): enable UDP measurements.
- `alternate_tcp` (bool, default: `true`): alternate TCP/UDP each cycle; if false, run both each cycle.
- `max_sample_duration` (duration, default: `10s`): per-sample timeout.
- `max_cycle_duration` (duration, default: `30s`): per-cycle timeout.
- `fast_start_timeout` (duration, default: `500ms`): fast-start probe timeout.
- `warmup_duration` (duration, default: `15s`): warmup period for relaxed switching.
- `stale_threshold` (duration, default: `120s`): metric staleness threshold.
- `fallback_to_icmp` (bool, default: `true`): allow ICMP-only fallback when measurements fail.

Notes:
- Legacy `target_bandwidth_up`/`target_bandwidth_down` are accepted and migrated to the per-protocol fields.

## scoring
## scoring

- `ema_alpha` (float, default: `0.2`): EMA smoothing factor in `(0,1]`.
- `ref_bandwidth_up` (string, default: `10m`): reference uplink bandwidth.
- `ref_bandwidth_down` (string, default: `50m`): reference downlink bandwidth.
- `ref_rtt_ms` (float, default: `50`): RTT reference in ms.
- `ref_jitter_ms` (float, default: `10`): jitter reference in ms.
- `ref_retrans_rate` (float, default: `0.01`): TCP retrans rate reference.
- `ref_loss_rate` (float, default: `0.01`): UDP loss rate reference.
- `weights_tcp` (object): per-metric weights for TCP scoring.
  - `bandwidth_up` (float, default: `0.15`)
  - `bandwidth_down` (float, default: `0.25`)
  - `rtt` (float, default: `0.25`)
  - `jitter` (float, default: `0.10`)
  - `retrans` (float, default: `0.25`)
- `weights_udp` (object): per-metric weights for UDP scoring.
  - `bandwidth_up` (float, default: `0.10`)
  - `bandwidth_down` (float, default: `0.30`)
  - `rtt` (float, default: `0.15`)
  - `jitter` (float, default: `0.30`)
  - `loss` (float, default: `0.15`)
- `protocol_weight_tcp` (float, default: `0.5`): overall blend weight for TCP.
- `protocol_weight_udp` (float, default: `0.5`): overall blend weight for UDP.
- `utilization_enabled` (bool, default: `true`): enable utilization penalty.
- `utilization_min_mult` (float, default: `0.3`): minimum multiplier.
- `utilization_threshold` (float, default: `0.7`): threshold `u^0` for penalty.
- `utilization_exponent` (float, default: `2`): exponent `p` for penalty curve.
- `utilization_window_sec` (int, default: `5`): utilization sampling window in seconds.
- `utilization_update_sec` (int, default: `1`): utilization sampling granularity in seconds.
- `bias_kappa` (float, default: `0.693`): bias multiplier coefficient.

Notes:
- Weights are normalized to sum to `1` if they do not already.
- Legacy fields `metric_ref_*` and `weights` are accepted for migration.

## switching

- `confirm_duration` (duration, default: `15s`): score gap must persist for this duration before switching.
- `failure_loss_threshold` (float, default: `0.2`): UDP loss threshold for immediate failover.
- `failure_retrans_threshold` (float, default: `0.2`): TCP retrans threshold for immediate failover.
- `switch_threshold` (float, default: `5`): score delta required to consider a switch.
- `min_hold_seconds` (int, default: `30`): minimum time to hold the active upstream before switching.
- `close_flows_on_unusable` (bool, default: `false`): close pinned flows on unusable transitions.

Notes:
- `confirm_windows` is still accepted for backward compatibility and is migrated into `confirm_duration`.

## limits

- `max_tcp_conns` (int, default: `50`): maximum concurrent TCP connections.
- `max_udp_mappings` (int, default: `500`): maximum concurrent UDP mappings.

## timeouts

- `tcp_idle_seconds` (int, default: `60`): close TCP conns after idle.
- `udp_idle_seconds` (int, default: `30`): expire UDP mappings after idle.

## control

- `addr` (string, default: `127.0.0.1`): control server bind address.
- `port` (int, default: `8080`): control server port.
- `token` (string, required): bearer token for `/rpc` and `/status`.

## webui

- `enabled` (bool, default: `true`): serve the embedded SPA.

## shaping

- `enabled` (bool, default: `false`): enable tc-based shaping.
- `device` (string, required when enabled): interface to shape (e.g. `eth0`).
- `ifb` (string, default: `ifb0`): IFB device used for ingress shaping.
- `aggregate_bandwidth` (string, default: `1g`): root cap for each direction.
  - Format: `100`, `100k`, `100m`, `1g` (bits/sec, SI units).

`egress` / `ingress` fields (defined on listeners or upstreams):
- `rate` (string, required): guaranteed rate (bits/sec).
- `ceil` (string, optional): maximum rate, defaults to `rate`.
- `burst` (string, optional): burst size in bytes (e.g. `16k`).
- `cburst` (string, optional): ceil burst size in bytes.

Notes:
- Shaping uses HTB + fq_codel and requires `CAP_NET_ADMIN` (or root).
- Ingress is handled via IFB redirection; existing root/ingress qdiscs are reset
  on the configured device and IFB.
- On shutdown, the program clears root/ingress qdiscs on the configured device
  and IFB.
- Both IPv4 and IPv6 traffic is supported for upstream shaping.
- Listener shaping uses port-based flower filters (priority 1, IPv4 only).
- Upstream shaping uses IP-based flower filters (priority 2, IPv4 or IPv6 based on upstream address).
