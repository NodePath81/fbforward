# Network Tools Monorepo

This repository contains two Linux-only networking tools built in Go plus a measurement server binary.

## fbforward

TCP/UDP port forwarder that selects the best upstream using fbmeasure-derived
TCP/UDP metrics, with ICMP used for reachability only. It exposes Prometheus metrics, a token-protected
RPC API, WebSocket status stream, and an embedded single-page Web UI.

Behavior highlights:

- NAT-style forwarding: clients connect to fbforward; upstream sees fbforward as source.
- Multiple listeners, single global upstream list; outbound port matches listener port.
- Probing uses fbmeasure RTT/jitter/retransmission/loss measurements for scoring; ICMP is reachability-only.
- Auto mode uses time-based confirmation, score threshold, and a minimum hold time; manual mode rejects unusable tags; optional coordination mode applies shared picks from `fbcoord` and falls back to local auto behavior when no valid coordinated pick is available.
- Fast failover triggers on loss/retrans thresholds or consecutive dial failures.
- TCP/UDP flows are pinned to the selected upstream until idle/expired.

fbforward relies on the `fbmeasure` server binary running on each upstream host
to provide targeted TCP/UDP measurement endpoints.
Coordination mode is optional and depends on a separate `fbcoord` service
deployed on Workers with Durable Objects.

Docs: `doc/` (start with `doc/project-overview.md`, `doc/user-guide-fbforward.md`, and `doc/configuration-reference.md`).

## bwprobe

Network quality measurement tool that runs repeatable, sample-based transfers at
a target bandwidth cap.

Docs: `doc/user-guide-bwprobe.md`.

## Requirements

- Linux only.
- Go toolchain: 1.25.5+ (per `go.mod`).
- fbforward:
  - ICMP probing requires `CAP_NET_RAW` (e.g., `sudo setcap cap_net_raw+ep ./build/bin/fbforward`).
  - Traffic shaping (optional) requires `CAP_NET_ADMIN`.
  - Web UI build requires Node.js + npm (see `web/package.json`).

## Build

```
make build            # build all binaries
make build-fbforward  # build fbforward only (builds UI if available)
make build-bwprobe    # build bwprobe only
make build-fbmeasure  # build fbmeasure only

# Or build directly:
go build ./cmd/fbforward
go build ./bwprobe/cmd
go build ./cmd/fbmeasure
```

Outputs:
- `build/bin/fbforward`
- `build/bin/bwprobe`
- `build/bin/fbmeasure`

## fbforward config (YAML)

Minimal example:

```yaml
forwarding:
  listeners:
    - bind_addr: 0.0.0.0
      bind_port: 9000
      protocol: tcp
    - bind_addr: 0.0.0.0
      bind_port: 9000
      protocol: udp
upstreams:
  - tag: primary
    destination:
      host: 203.0.113.10
  - tag: backup
    destination:
      host: example.net
control:
  bind_addr: 127.0.0.1
  bind_port: 8080
  auth_token: "replace-with-a-long-random-token"
  webui:
    enabled: true
  metrics:
    enabled: true

# Optional coordination mode via fbcoord
coordination:
  endpoint: https://fbcoord.example.workers.dev
  pool: default
  node_id: fbforward-01
  token: "replace-with-a-separate-long-random-token"
  heartbeat_interval: 10s
```

Use a random token with at least 16 characters. The placeholder value
`change-me` is rejected at startup.

See `doc/configuration-reference.md` for the full schema (`forwarding`, `upstreams`, `dns`, `reachability`, `measurement`, `scoring`, `switching`, `control`, `coordination`, `shaping`).

## Run (fbforward)

```
cp configs/config.example.yaml config.yaml
./build/bin/fbforward --config config.yaml
```

## Deploy fbmeasure

For upstream hosts, build and run the supplied runtime image:

```bash
podman build -f deploy/container/fbmeasure/Containerfile -t fbmeasure:latest .
podman run -d --name fbmeasure \
  --restart unless-stopped \
  -p 9876:9876/tcp \
  -p 9876:9876/udp \
  fbmeasure:latest
```

The same `Containerfile` works with Docker by replacing `podman` with `docker`.

For secure deployments, enable TLS on `fbmeasure` with
`--tls-cert-file/--tls-key-file` and configure `measurement.security` in
fbforward. See `doc/user-guide-fbmeasure.md` and
`doc/configuration-reference.md`.

## Debian packaging (fbforward)

```
# Build a .deb (from repo root)
deploy/packaging/debian/build.sh
```

Prereqs:
- `dpkg-deb` (from `dpkg` package)
- Go toolchain (for building `fbforward` if the binary is not already present)
- systemd (for install/enable on target host)
