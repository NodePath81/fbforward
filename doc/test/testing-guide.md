# Testing guide

The current test surface is intentionally local and package-oriented. There is
no distributed coordinator, browser build, or repository-owned manual lab.

## Automated tests

Run the complete Go suite:

```bash
GOCACHE=/tmp/fbforward-gocache go test ./...
GOCACHE=/tmp/fbforward-gocache go vet ./...
```

Useful focused checks include:

```bash
GOCACHE=/tmp/fbforward-gocache go test -race \
  ./internal/config ./internal/upstream ./internal/control \
  ./internal/app ./internal/metrics ./internal/forwarding
```

Tests cover configuration validation, route-local upstream selection, health
and RTT state, control RPCs, audit storage, Flow Context, firewall behavior,
forwarding, and Prometheus output.

## Integration tests

Linux integration scenarios should cover:

- simple TCP forwarding;
- simple UDP forwarding;
- firewall rejection and audit records;
- Flow Context resolve and tagging;
- persistent policy reload;
- online-rule TTL and expiry;
- adaptive fallback;
- manual route-boundary selection.

Normal integration tests must not require root unless a kernel namespace or
traffic-shaping scenario explicitly needs it.

## API-only control plane

The control plane has no embedded frontend. Verify the API-only root and the
remaining endpoints with an HTTP client:

- `/` returns 404;
- `/rpc`, `/identity`, and `/metrics` require Bearer authentication;
- `/status` continues to provide the authenticated WebSocket stream;
- `/flow-context/*` remains available when configured.

The repository no longer runs npm frontend builds or removed external-control
tests.
