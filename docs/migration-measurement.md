# Measurement Migration Guide (ICMP to bwprobe)

This guide describes how to migrate fbforward from legacy ICMP-based scoring
to the TCP/UDP measurement pipeline. ICMP remains reachability-only.

## Prerequisites

- Build and deploy the measurement server on each upstream host:
  - `make build-fbmeasure`
  - or `go build -o build/bin/fbmeasure ./bwprobe/cmd/fbmeasure`
- Start the server on each upstream:
  - `./build/bin/fbmeasure --port 9876`
- Ensure firewall rules allow the measurement port.

## Configuration changes

1) Add the `measurement` block with bandwidth targets and timeouts.
2) Keep `probe` for ICMP reachability, but do not use it for scoring.
3) Update `scoring` to the new TCP/UDP weight structure.
4) Update `switching` to use `confirm_duration`.
5) Optionally set `measure_host`, `measure_port`, `priority`, and `bias` per
   upstream.

### Example updates

```yaml
measurement:
  interval: 2s
  tcp_target_bandwidth_up: 10m
  tcp_target_bandwidth_down: 50m
  udp_target_bandwidth_up: 10m
  udp_target_bandwidth_down: 50m
  sample_bytes: 500KB
  tcp_enabled: true
  udp_enabled: true
  alternate_tcp: true
  fallback_to_icmp: true

scoring:
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

switching:
  confirm_duration: 15s
  failure_loss_threshold: 0.2
  failure_retrans_threshold: 0.2
```

## Legacy field migration

- `scoring.metric_ref_*` is deprecated. Use `scoring.ref_*` instead.
- `scoring.weights` is deprecated. Use `weights_tcp` and `weights_udp`.
- `switching.confirm_windows` is deprecated. Use `confirm_duration`.
- `probe.interval` may be reused as `measurement.interval` if not set. A
  warning is logged when migration occurs.
- Legacy `target_bandwidth_up`/`target_bandwidth_down` are migrated to
  `tcp_target_bandwidth_*` and `udp_target_bandwidth_*` when set.

## Validation checklist

- `fbmeasure` is reachable on each upstream host.
- `fbforward` starts and reports bandwidth/score metrics in `/metrics`.
- Web UI shows bandwidth, loss/retrans, and TCP/UDP scores per upstream.
