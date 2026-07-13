# Network Tools Monorepo

This repository contains Linux-only networking tools built in Go and a
measurement server binary.

## fbforward

TCP/UDP port forwarder that selects route-local upstreams using fbmeasure health
and RTT observations. Optional features include GeoIP-based lookups, persisted
IP flow and rejection logging, and CIDR/ASN/country firewalling. It exposes
Prometheus metrics and a token-protected RPC API. Active flows are exposed by
the authenticated `GetActiveFlows` RPC for lightweight polling clients.

Behavior highlights:

- NAT-style forwarding: clients connect to fbforward; upstream sees fbforward as source.
- Multiple listeners, single global upstream list; outbound port matches listener port.
- Probing uses fbmeasure TCP/UDP RTT observations to update one unified health state.
- Auto mode selects within each route using health, RTT, priority, and configuration order; manual mode pins a usable upstream.
- Fast failover is based on health state and dial cooldown.
- TCP/UDP flows are pinned to the selected upstream until idle/expired.

fbforward relies on the `fbmeasure` server binary running on each upstream host
to provide targeted TCP/UDP measurement endpoints. Cross-node selection is
intentionally out of scope; use route-local configuration for each node.

Docs: `doc/` (start with `doc/project-overview.md`, `doc/user-guide-fbforward.md`, `doc/configuration-reference.md`, and `doc/notification-events.md`).

## bwprobe

Network quality measurement tool that runs repeatable, sample-based transfers at
a target bandwidth cap.

Docs: `doc/user-guide-bwprobe.md`.

## fbnotify

Standalone Cloudflare Worker notification bridge with an operator UI and admin
API for provider targets, routing, node tokens, operator-token rotation,
provider test-send flows, and a built-in capture inbox.

Docs: `doc/fbnotify/index.md` and `doc/notification-events.md`.

## Requirements

- Linux only.
- Go toolchain: 1.25.5+ (per `go.mod`).
- fbforward:
  - Traffic shaping (optional) requires `CAP_NET_ADMIN`.
  - fbforward currently links `github.com/mattn/go-sqlite3` for IP-log support, so building `fbforward` requires a working C toolchain (gcc) on the build host.

## Build

```
make build            # build all binaries
make build-fbforward  # build fbforward only
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
  metrics:
    enabled: true

```

Use a random token with at least 16 characters. The placeholder value
`change-me` is rejected at startup.

See `doc/configuration-reference.md` for the full schema (`listeners`, `routes`,
`upstreams`, `dns`, `measurement`, `health`, `control`, `logging`, `shaping`,
`geoip`, `ip_log`, `flow_context`, `firewall`).

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
