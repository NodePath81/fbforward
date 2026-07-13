# Testing guide

Use the small, repeatable test targets during development:

```bash
make test       # unit and contract tests
make test-e2e   # seven loopback product paths, no external network
make test-race  # concurrency-sensitive domains
```

Before a release also run `go vet ./...`, `node --check web/app.js`, and
`git diff --check`. Data-plane unit tests use fake policies, selectors,
observers, and local GeoIP readers; they do not require a control server,
SQLite, external network, host-level traffic controls, or privileged
capabilities.

The E2E harness starts only real local fbforward processes and loopback echo
servers. It covers startup/control, static TCP and UDP, firewall rejection,
online-rule expiry, Flow Context tagging, and adaptive fallback. Each wait has
a deadline; no dashboard, browser, container, root access, or long fixed sleep
is part of ordinary CI. `make test-manual` is reserved for phase 15 system
integration experiments.

Host traffic shaping and GeoIP downloads are deployment concerns. Test the
GeoIP update script separately with a local HTTP fixture and verify that
`ReloadGeoIP` reopens the atomically replaced files.
