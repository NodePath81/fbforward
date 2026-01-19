# Network Tools Monorepo

This repository contains two Linux-only networking tools built in Go plus a measurement server binary.

## fbforward

TCP/UDP port forwarder that selects the best upstream using bwprobe-derived
TCP/UDP metrics, with ICMP used for reachability only. It exposes Prometheus metrics, a token-protected
RPC API, WebSocket status stream, and an embedded single-page Web UI.

Behavior highlights:

- NAT-style forwarding: clients connect to fbforward; upstream sees fbforward as source.
- Multiple listeners, single global upstream list; outbound port matches listener port.
- Probing uses bwprobe measurements for scoring; ICMP is reachability-only.
- Auto mode uses time-based confirmation, score threshold, and a minimum hold time; manual mode rejects unusable tags.
- Fast failover triggers on loss/retrans thresholds or consecutive dial failures.
- TCP/UDP flows are pinned to the selected upstream until idle/expired.

fbforward relies on the `fbmeasure` server binary running on each upstream host
to provide TCP/UDP measurement endpoints.

Docs: `docs/README.md` (see `docs/codebase.md` and `docs/configuration.md`).

## bwprobe

Network quality measurement tool that runs repeatable, sample-based transfers at
a target bandwidth cap.

Docs: `docs/bwprobe/` (start with `docs/bwprobe/readme.md`).

## Requirements

- Linux only.
- Go toolchain: 1.25.5+ (per `go.mod`).
- fbforward:
  - ICMP probing requires `CAP_NET_RAW` (e.g., `sudo setcap cap_net_raw+ep ./build/bin/fbforward`).
  - Traffic shaping (optional) requires `CAP_NET_ADMIN`.
  - Web UI build requires Node.js + npm (see `web/package.json`).

## Build

```
make build            # build both binaries
make build-fbforward  # build fbforward only (builds UI if available)
make build-bwprobe    # build bwprobe only
make build-fbmeasure  # build fbmeasure only

# Or build directly:
go build ./cmd/fbforward
go build ./bwprobe/cmd
go build ./bwprobe/cmd/fbmeasure
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
  auth_token: "change-me"
  webui:
    enabled: true
  metrics:
    enabled: true
```

See `docs/configuration.md` for the full schema (`forwarding`, `upstreams`, `dns`, `reachability`, `measurement`, `scoring`, `switching`, `control`, `shaping`).

## Run (fbforward)

```
cp configs/config.example.yaml config.yaml
./build/bin/fbforward --config config.yaml
```

## Debian packaging (fbforward)

```
# Build a .deb (from repo root)
deploy/packaging/debian/build.sh
```

Prereqs:
- `dpkg-deb` (from `dpkg` package)
- Go toolchain (for building `fbforward` if the binary is not already present)
- systemd (for install/enable on target host)
