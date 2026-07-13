# Developer guide

fbforward is a Go service with two runtime helpers: `fbforward` and the
`fbmeasure` sidecar. The data path is in `internal/forwarding`; Runtime wiring
is in `internal/app`; health state and route-local selection are in
`internal/upstream`; control RPC and polling status are in `internal/control`.

Audit, Flow Context, policy, GeoIP, notification, and the dependency-free
operator page are separate packages. GeoIP databases are deployment artifacts:
the process reads local MMDB files and `ReloadGeoIP` atomically reopens them.
Webhook delivery is bounded and asynchronous so it never blocks forwarding.

## Development checks

```bash
go test ./...
go test -race ./internal/forwarding ./internal/control ./internal/upstream
go vet ./...
node --check web/app.js
git diff --check
```

Keep one Flow pinned to one upstream for its lifetime. New policy, route, and
health changes affect new Flows only. Prefer small interfaces and fake-based
tests over starting the complete Runtime.
