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
- ICMP probing requires `CAP_NET_RAW` (e.g., `sudo setcap cap_net_raw+ep ./build/bin/fbforward`).
- Traffic shaping (if enabled) requires `CAP_NET_ADMIN`.
- Go toolchain: `1.25.4` (per `go.mod`).
- Go module deps: `github.com/gorilla/websocket@v1.5.3`, `github.com/vishvananda/netlink@v1.3.1`, `golang.org/x/net@v0.33.0`, `gopkg.in/yaml.v3@v3.0.1`, `golang.org/x/sys@v0.28.0`, `github.com/vishvananda/netns@v0.0.5` (indirect).
- Frontend build deps: Node.js + npm with `typescript@^5.4.0`, `vite@^5.4.0` (see `web/package.json`).

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

Supported fields include: `resolver.servers`, `probe.interval/window_size/discovery_delay`, `scoring.ema_alpha/metric_ref_*/weights`, `switching.confirm_windows/failure_loss_threshold/switch_threshold/min_hold_seconds`, `limits.max_tcp_conns/max_udp_mappings`, `timeouts.tcp_idle_seconds/udp_idle_seconds`, `webui.enabled`, `shaping.enabled/device/ifb/aggregate_bandwidth`, and `listeners.ingress/egress`.

## Run

```
cp configs/config.example.yaml config.yaml
./build/bin/fbforward --config config.yaml
```

## Build

```
# Build UI + Go binary
make

# Or build only the Go binary (uses existing web/dist)
go build ./cmd/fbforward
```

## Debian packaging

```
# Build a .deb (from repo root)
deploy/packaging/debian/build.sh
```

Prereqs:
- `dpkg-deb` (from `dpkg` package)
- Go toolchain (for building `fbforward` if the binary is not already present)
- systemd (for install/enable on target host)
