# Testing guide

Run `go test ./...`, `go test -race ./...`, `go vet ./...`, and
`node --check web/app.js` before a release. Data-plane tests use loopback and
fake policies, selectors, observers, and local GeoIP readers; they do not
require a control server, SQLite, external network, tc, or privileged
capabilities.

Host traffic shaping and GeoIP downloads are deployment concerns. Test the
GeoIP update script separately with a local HTTP fixture and verify that
`ReloadGeoIP` reopens the atomically replaced files.
