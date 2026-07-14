# Development and testing

fbforward is a Go service with two binaries: `fbforward` and the `fbmeasure`
probe server. Data-plane code is in `internal/forwarding`; Runtime wiring is
in `internal/app`; route selection and health are in `internal/upstream`;
ControlServer code is in `internal/control`; Flow, audit, policy, GeoIP,
notification, and Flow Context are separate packages. The operator page is a
dependency-free embedded client.

## Development checks

```bash
make test
make test-e2e
make test-race
go vet ./...
node --check web/app.js
git diff --check
```

The E2E suite uses loopback services and real local fbforward processes. It
does not require root, containers, external network access, a browser, or
host traffic-control modules. Manual system tests belong to `make test-manual`.

## Test boundaries

Keep high-value unit tests for Flow lifecycle, concurrent close, TCP/UDP byte
direction, routing, health transitions, policy precedence, SQLite migration
and transaction rollback, tags, GeoIP reload, webhook retry/drop behavior,
and Flow Context authorization. E2E tests should prove that major components
are wired together, not repeat every parser or error permutation.

Data-plane tests use fake policies, selectors, observers, and local readers.
Audit tests use temporary SQLite databases. E2E tests use deadlines instead of
long fixed sleeps and inspect temporary SQLite events when that is the most
direct evidence.

## Invariants

- A TCP stream or UDP mapping is one Flow.
- A Flow selects one upstream once and remains pinned until close.
- Updates use cumulative counters and close is idempotent.
- Persistent policy reload and online-rule changes affect new Flows only.
- Audit writes represent complete Flow lifecycles; packet records are not
  stored.
- Forwarding does not depend on SQLite, WebSocket, GeoIP, or control-server
  implementations; use small interfaces and fake-based tests.

## Style

Prefer small cohesive packages and explicit interfaces over broad helpers.
Keep parser errors short and actionable. Keep HTTP errors JSON and avoid
leaking SQL or credentials. Use structured event names and stable attributes
for operational logs. Comments should explain an invariant or a non-obvious
tradeoff, not restate the code.

## Change workflow

Run the narrowest relevant test after each change, then run the full checks
before a commit. Use Angular/Conventional Commit subjects such as
`fix(audit): ...`, `test(flow): ...`, and `docs: ...`. Do not mix production
changes with unrelated documentation or test cleanup.
