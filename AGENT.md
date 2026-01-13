# Agent Guide

Short, practical notes for working on this repo.

## Project summary

`fbforward` is a Linux-only Go userspace NAT-style TCP/UDP forwarder. It probes upstreams via ICMP, scores them, and forwards new flows to the best upstream. It exposes a token-protected control plane, Prometheus metrics, WebSocket status streaming, and an embedded SPA UI.

Before making structural changes, skim `docs/codebase.md` for a concise architecture and component overview.

## Scoring and selection

- Probing is ICMP only. Each upstream accumulates a fixed-size window of probe results and computes:
  - loss = lost / window_size (clamped to `[0,1]`)
  - avg RTT from successful probes
  - jitter = mean absolute difference between consecutive RTT samples
- Window metrics are smoothed per upstream using EMA: `metric = a*new + (1-a)*old`.
- Each window produces subscores `exp(-metric / metric_ref)` and an overall score:

```
score = 100 * (s_rtt ^ w_rtt) * (s_jit ^ w_jit) * (s_los ^ w_los)
```

- Upstream usability is `loss < 1` (100% loss marks unusable).
- Auto mode: best-score switching uses confirmation windows, a score delta threshold, and a minimum hold time.
- Fast failover: if the active upstream hits the loss threshold or repeated dial failures, switch immediately.
- Manual mode: operator-selected tag must be usable; otherwise selection is rejected.
- Dial failures trigger a short cooldown that temporarily removes the upstream from selection.

## Key paths

- `cmd/fbforward/main.go`: entrypoint and OS guard.
- `internal/app/runtime.go`: lifecycle wiring, DNS refresh loop, listener/probe startup.
- `internal/probe/probe.go`: ICMP probing and window metrics.
- `internal/forwarding/forward_tcp.go`, `internal/forwarding/forward_udp.go`: data plane forwarding.
- `internal/upstream/upstream.go`: selection/scoring state and dial-failure cooldown.
- `internal/control/control.go`: control API, metrics auth, WebSocket status.
- `web/handler.go` + `web/`: embedded UI assets.
- `internal/config/config.go`: YAML config defaults/validation.
- `docs/configuration.md`: config reference.

## Build and run

```
go build ./...
./build/bin/fbforward --config config.yaml
```

ICMP probing requires `CAP_NET_RAW`:

```
sudo setcap cap_net_raw+ep ./build/bin/fbforward
```

## Control plane auth

- RPC and metrics require `Authorization: Bearer <token>`.
- WebSocket `/status` uses subprotocols:
  - `fbforward`
  - `fbforward-token.<base64url(token)>`

## UI assets

Static files live in `web/` and are embedded via `web/handler.go`. If you add files there, keep names ASCII and update the UI code as needed. No external asset pipeline.

## Config hints

- Listener ports must be `1..65535`.
- Duplicate `addr:port:protocol` listeners are rejected.
- Hostname upstreams are re-resolved every `30s` (see `internal/app/runtime.go`).

## Testing

No automated tests yet. Prefer `go test ./...` and a quick manual run with a local config.
