# fbforward contributor guide

fbforward is a small Go TCP/UDP forwarder. It selects one upstream per Flow,
uses fbmeasure for health and RTT on adaptive routes, and exposes a bearer-
protected ControlServer API plus Prometheus metrics. The embedded operator UI
is plain HTML/CSS/JavaScript and uses polling RPC snapshots.

## Layout

- `cmd/fbforward`: process entry point.
- `internal/app`: runtime wiring and lifecycle.
- `internal/forwarding`: TCP/UDP data plane and route-aware picker interfaces.
- `internal/upstream`: upstream definitions, health snapshots, and selectors.
- `internal/measure` and `internal/fbmeasure`: probe scheduling and protocol.
- `internal/control`: HTTP RPC, middleware, audit, and status projection.
- `internal/audit`: SQLite audit store and asynchronous pipeline.
- `internal/flowcontext`: backend tuple registry and tag API.
- `internal/policy`: persistent and online policy providers.
- `internal/geoip`: local MMDB readers and atomic reload.
- `web`: dependency-free operator UI.

## Common commands

```bash
make build
go test ./...
go test -race ./internal/forwarding ./internal/control ./internal/upstream
go vet ./...
node --check web/app.js
```

Keep changes focused, preserve Flow pinning, and do not add external network
downloads to the runtime. GeoIP updates belong in deployment automation; the
process only reloads already replaced local files through `ReloadGeoIP`.
