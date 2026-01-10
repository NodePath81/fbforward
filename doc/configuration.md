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
scoring:
  ema_alpha: 0.357
  metric_ref_rtt_ms: 7
  metric_ref_jitter_ms: 1
  metric_ref_loss: 0.05
  weights:
    rtt: 0.2
    jitter: 0.45
    loss: 0.35
switching:
  confirm_windows: 3
  failure_loss_threshold: 0.8
  switch_threshold: 1.0
  min_hold_seconds: 5
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
- `probe` (object, optional): ICMP probe scheduling.
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

## probe

- `interval` (duration, default: `1s`): ICMP probe interval per upstream.
- `window_size` (int, default: `5`): number of probes per scoring window.
- `discovery_delay` (duration, default: `window_size * interval`): wait before picking initial upstream.

Duration format:
- String: Go duration (e.g., `500ms`, `2s`, `1m`).
- Number: seconds (int/float).

## scoring

- `ema_alpha` (float, default: `0.357`): EMA smoothing factor in `(0,1]`.
- `metric_ref_rtt_ms` (float, default: `7`): RTT reference in ms.
- `metric_ref_jitter_ms` (float, default: `1`): jitter reference in ms.
- `metric_ref_loss` (float, default: `0.05`): loss reference as fraction.
- `weights` (object, optional): weights for RTT/jitter/loss subscores.
  - `rtt` (float, default: `0.2`)
  - `jitter` (float, default: `0.45`)
  - `loss` (float, default: `0.35`)

Notes:
- Weights are normalized to sum to `1` if they do not already.
- Loss is clamped to `[0,1]` before scoring.
- Jitter is the mean absolute difference between consecutive RTT samples in a window.

## switching

- `confirm_windows` (int, default: `3`): number of best-score windows required before switching.
- `failure_loss_threshold` (float, default: `0.8`): if active loss in a window is at/above this, fail over immediately.
- `switch_threshold` (float, default: `1.0`): score delta required to consider a switch.
- `min_hold_seconds` (int, default: `5`): minimum time to hold the active upstream before score-based switching.

Notes:
- `failure_loss_threshold` affects fast failover only; usability still uses loss == 1.

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
