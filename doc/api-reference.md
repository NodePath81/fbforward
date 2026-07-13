# API reference

fbforward exposes a token-protected JSON-RPC API on the ControlServer HTTP
listener. Requests use `POST /rpc` with a JSON body containing `method` and
`params`; responses retain the `{ok,result,error}` shape.

## Runtime and routing

- `GetStatus`, `GetActiveFlows`, `GetRouteStatus`
- `SetRouteOverride`, `ClearRouteOverride`
- `SetUpstream` (deprecated compatibility wrapper for single-route setups)
- `ListUpstreams`, `RunMeasurement`
- `GetRuntimeConfig`, `GetMeasurementConfig`, `GetScheduleStatus`
- `Restart`, `SendTestNotification`

Route-local overrides affect new flows only. Adaptive routes fall back within
their configured upstream set when an override is unavailable; static routes do
not automatically fail over.

## Health and GeoIP

- `GetGeoIPStatus`
- `ReloadGeoIP`

GeoIP files are downloaded and atomically replaced by deployment automation.
`ReloadGeoIP` only reopens local MMDB paths and never performs network I/O.

## Audit and policy

- `GetIPLogStatus`, `QueryIPLog`, `QueryRejectionLog`, `QueryLogEvents`
- `GetTopTalkers`, `GetTopASNs`, `QueryAudit`
- `QueryAudit` uses the restricted syntax documented in
  [`audit-query.md`](audit-query.md); it never executes caller-provided SQL.
- `GetFirewallPolicy`, `GetFirewallStatus`, `ValidateFirewallPolicy`,
  `ReloadFirewallPolicy`
- `CreateOnlineRule`, `ListOnlineRules`, `DeleteOnlineRule`,
  `ExpireOnlineRule`

## Flow Context

When enabled, the same HTTP listener exposes:

- `POST /flow-context/resolve`
- `POST /flow-context/rpc`

Backend integrations may use the small synchronous Go client described in
[`flow-context-client.md`](flow-context-client.md) to resolve accepted socket
connections without constructing tuple JSON manually.

Flow Context identities use dedicated bearer tokens and route/upstream scopes.

## Metrics and identity

- `GET /metrics` returns Prometheus metrics when enabled.
- `GET /identity` returns instance identity for the operator UI.

All control endpoints require the configured bearer token. Protect remote
control traffic with TLS termination or a trusted private network.
