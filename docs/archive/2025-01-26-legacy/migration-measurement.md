# Measurement Migration Guide (ICMP to bwprobe)

This guide describes how to migrate fbforward from legacy ICMP-based scoring
to the TCP/UDP measurement pipeline. ICMP remains reachability-only.
It assumes the V2 configuration layout; legacy field migration is not supported.

## Prerequisites

- Build and deploy the measurement server on each upstream host:
  - `make build-fbmeasure`
  - or `go build -o build/bin/fbmeasure ./bwprobe/cmd/fbmeasure`
- Start the server on each upstream:
  - `./build/bin/fbmeasure --port 9876`
- Ensure firewall rules allow the measurement port.

## Configuration changes

1) Add the `measurement` block with protocol parameters and scheduling.
2) Keep `reachability` for ICMP probes, but do not use ICMP for scoring.
3) Update `scoring` to protocol-specific references and weights.
4) Update `switching` to the auto/failover layout.
5) Optionally set per-upstream measurement host/port, priority, and bias.

## Example updates

```yaml
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

switching:
  auto:
    confirm_duration: 15s
    score_delta_threshold: 5
    min_hold_time: 30s
  failover:
    loss_rate_threshold: 0.2
    retransmit_rate_threshold: 0.2
```

## Validation checklist

- `fbmeasure` is reachable on each upstream host.
- `fbforward` starts and reports bandwidth/score metrics in `/metrics`.
- Web UI shows bandwidth, loss/retrans, and TCP/UDP scores per upstream.
