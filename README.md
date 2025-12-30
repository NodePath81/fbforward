# fbforward

Linux-only userspace TCP/UDP port forwarder that picks the best upstream using ICMP-derived quality (RTT, jitter, loss). Includes Prometheus metrics, a token-protected RPC API, WebSocket status stream, and an embedded single-page Web UI.

## Behavior highlights

- NAT-style forwarding: clients connect to fbforward; upstream sees fbforward as source.
- Multiple listeners, single global upstream list; outbound port always matches listener port.
- Probing is ICMP-only; upstream is unusable on 100% loss in a window and recovers automatically.
- Auto mode uses confirmation windows, score threshold, and a minimum hold time; manual mode rejects unusable tags.
- Fast failover triggers on high loss windows or consecutive dial failures.
- TCP/UDP flows are pinned to the selected upstream until idle/expired.

## Requirements

- Linux only.
- ICMP probing requires `CAP_NET_RAW` (e.g., `sudo setcap cap_net_raw+ep ./fbforward`).

## Control plane

- `GET /metrics` Prometheus metrics (Bearer token required).
- `POST /rpc` JSON RPC: `SetUpstream`, `Restart`, `GetStatus`, `ListUpstreams` (Bearer token required).
- `GET /status` WebSocket stream (token required; browser UI uses WebSocket subprotocol).
- `GET /` embedded SPA UI.

## Config (YAML)

Minimal example:

```yaml
listeners:
  - addr: 0.0.0.0
    port: 9000
    protocol: tcp
  - addr: 0.0.0.0
    port: 9000
    protocol: udp
upstreams:
  - tag: primary
    host: 203.0.113.10
  - tag: backup
    host: example.net
control:
  addr: 127.0.0.1
  port: 8080
  token: "change-me"
```

Supported fields include: `resolver.servers`, `probe.interval/window_size/discovery_delay`, `scoring.ema_alpha/metric_ref_*/weights`, `switching.confirm_windows/failure_loss_threshold/switch_threshold/min_hold_seconds`, `limits.max_tcp_conns/max_udp_mappings`, `timeouts.tcp_idle_seconds/udp_idle_seconds`, and `webui.enabled`.

## Run

```
./fbforward --config config.yaml
```
