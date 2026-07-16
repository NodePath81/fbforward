# API reference

The ControlServer uses HTTP. The root path serves the embedded static operator
page; its data requests are still authenticated. Control and operator
endpoints require the configured bearer token. RPC requests are `POST /rpc`
with `{method, params}`; responses keep the `{ok, result, error}` shape.
Errors are JSON and requests are authenticated, rate-limited, size-limited,
and audited.

## Common contract

Successful RPC responses have this form:

```json
{"ok": true, "result": {}}
```

Failures have this form:

```json
{"ok": false, "error": "message"}
```

The control token is sent only as `Authorization: Bearer <token>`. The RPC
body is bounded at 1 MiB. Unknown methods, malformed JSON, and invalid
parameters are rejected before business execution. Typical statuses are
`400` for a bad request, `401` for missing or invalid credentials, `404` for
an unknown resource, `409` for a conflicting create, `413` for an oversized
body, `429` for rate limiting, and `503` when an optional runtime component is
unavailable. Every request receives a request ID and a completion audit
record.

## Runtime and routes

Read-only methods include `GetStatus`, `GetActiveFlows`, `ListFlowContextTags`, `ListFlowContextActions`, `GetRouteStatus`,
`ListUpstreams`, `GetRuntimeConfig`, `GetMeasurementConfig`,
`GetScheduleStatus`, and `GetIPLogStatus`. Runtime responses expose current
health and route-local state; legacy per-protocol ranking fields are not part
of the current response contract.

Route-local control methods are:

- `SetRouteOverride` with `{route, upstream}`;
- `ClearRouteOverride` with `{route}`;
- `RunMeasurement` with `{tag, protocol}`;
- `Restart` and `SendTestNotification`.

`GetRouteStatus` returns each route's strategy, configured upstreams,
`default_upstream`, `override_upstream`, `override_state`, and effective
upstream. `SetRouteOverride` rejects an unknown route or an upstream outside
that route. `ClearRouteOverride` removes only the selected route's override.

Overrides affect new Flows. Adaptive routes fall back within their configured
route when an override is unavailable; static routes do not automatically
fail over. `SetUpstream` remains only as a deprecated compatibility wrapper
for a single-route configuration and accepts `auto` or `manual`.

The principal runtime response groups are:

| Method | Purpose |
| --- | --- |
| `GetStatus` | Process, listener, and upstream status |
| `GetActiveFlows` | Current TCP streams and UDP mappings |
| `ListFlowContextTags` | Current unexpired Flow/Client Tag projections |
| `ListFlowContextActions` | Recent Flow Context set/unset events |
| `GetRouteStatus` | Route-local effective and override state |
| `ListUpstreams` | Configured addresses and health snapshots |
| `GetRuntimeConfig` | Sanitized loaded configuration; secrets omitted |
| `GetMeasurementConfig` | Effective probe and security settings |
| `GetScheduleStatus` | Adaptive probe queue and next-due state |
| `GetIPLogStatus` | SQLite availability, counts, and retention state |

`RunMeasurement` starts one requested probe asynchronously and accepts only a
configured upstream and enabled protocol. `Restart` schedules a runtime
restart rather than blocking the HTTP request. `SendTestNotification` returns
service unavailable when the webhook sink is disabled.

## Health, GeoIP, and metrics

- `GetGeoIPStatus` returns local database state.
- `ReloadGeoIP` reopens configured local MMDB files; it never downloads data.
- `GET /metrics` returns Prometheus metrics when enabled.
- `GET /identity` returns instance identity for the operator page.

Measurement returns health and raw RTT. TCP and UDP observations contribute to
one upstream health snapshot. Static-only routes do not start a scheduler.
`GetGeoIPStatus` reports local database availability; `ReloadGeoIP` only
reopens local files and never downloads them. `GET /metrics` is available when
enabled, and `/identity` returns the instance identity used by the operator
page.

## Audit and restricted query DSL

Audit methods are `GetIPLogStatus`, `QueryIPLog`, `QueryRejectionLog`,
`QueryLogEvents`, `GetTopTalkers`, `GetTopASNs`, and `QueryAudit`.

`QueryAudit` accepts a deliberately small language, never caller-provided SQL:

```text
flows tag=app:test since=-24h | sort bytes_total desc | limit 50
top asns tag=app:test since=-24h | sort bytes_total desc | limit 20
top tags since=-24h | sort bytes_total desc | limit 50
rejections protocol=tcp reason="connection limit" | limit 100
```

Sources are `flows`, `rejections`, `events`, `top clients`, `top asns`, and
`top tags`.
Filters are source-specific and use exact AND matching: `tag`, `protocol`,
`cidr`, `ip`, `asn`, `country`, `upstream`, `reason`, `since`, and `until`.
Pipeline stages are `sort field asc|desc`, `limit n`, and `offset n`.

The query is limited to 4096 bytes and 1000 rows. Values are bound SQL
parameters and sort fields come from an allowlist. IP/CIDR, protocol, country,
ASN, and time ranges are validated before querying. Errors include a byte
position and short reason. Top ASN results have one row per ASN; an ASN that
spans multiple countries is not split into multiple rows.

The direct top-list methods accept optional Unix-second `start_time` and
`end_time`, `protocol`, `upstream`, `tag`, `sort_by`, `sort_order`, `limit`,
and `offset`. Time ranges must be ordered, protocol must be `tcp` or `udp`,
and pagination is bounded. `GetTopTalkers` returns client IP, upload/download
bytes, total bytes, and Flow count. `GetTopASNs` returns ASN, aggregated bytes,
Flow count, and deterministic country/organization fields. Both the DSL and
direct methods execute filtering, aggregation, ordering, and pagination in
SQLite; callers never submit SQL text.

`top tags` returns `tag`, upload/download/total bytes, and Flow count. Its
aggregation uses current unexpired Flow and Client Tag projections. If a Flow
has the same Tag through both projections, that Flow is counted once for that
Tag; traffic may still contribute to multiple different Tags. Historical
set/unset state is not reconstructed.

`GetActiveFlows` includes an additive `tags` array on each Flow. Each item has
`tag` and `scope` (`flow` or `client`) and represents an unexpired current
projection. Traffic and Flow totals remain an Audit concern.

## Firewall and online rules

Persistent policy methods are `GetFirewallPolicy`, `GetFirewallStatus`,
`ValidateFirewallPolicy`, and `ReloadFirewallPolicy`. Reload compiles and
atomically swaps the policy; an invalid candidate leaves the current policy
active. `ValidateFirewallPolicy` accepts optional candidate YAML content and
does not change the active snapshot. `ReloadFirewallPolicy` reads the
configured policy file and affects new Flows only.

Online-rule methods are `CreateOnlineRule`, `ListOnlineRules`,
`DeleteOnlineRule`, and `ExpireOnlineRule`. Rules have bounded TTL and are
stored separately from the persistent policy. Create parameters include
`rule_id`, `action`, `matcher`, `priority`, `ttl_seconds`, `reason`, and
`ticket_ref`; the server supplies `created_by`. Matchers use AND semantics.
Actions are `deny`, `rate_limit`, and `route_override`; online allow is not
supported. Create, delete, and expire events are audited. Online deny cannot
be bypassed by a non-deny action. These methods return `503` when SQLite audit
storage is disabled.

## Flow Context

Flow Context uses the same TCP HTTP listener but dedicated backend identities.
The control token is not accepted. Protect remote calls with TLS termination
or a trusted private network.

Endpoints:

```text
POST /flow-context/resolve
POST /flow-context/rpc
```

Resolve accepts `protocol`, `backend_key`, `local_addr`, `remote_addr`, and an
optional bounded `wait_ms`. The service returns the original client address,
route, upstream, FlowID, and lifecycle state. A closed Flow remains resolvable
only during the Registry grace period; after that it is reported as not found.

`Ping` is an authenticated, side-effect-free health check. It returns
`{"pong":true}` and does not require a Flow or write SQLite. Tag RPC methods
are `ResolveFlow`, `SetFlowTag`, `UnsetFlowTag`,
`SetClientTag`, `UnsetClientTag`, and `ListFlowTags`. Tag writes contain a
`flow_id` or resolved client, `namespace`, `key`, `value`, and optional
`ttl_seconds`; namespaces and TTL are checked against the authenticated
identity and configured maximum. Backend identities have exact route,
upstream, and namespace permissions. Tag changes are persisted in SQLite
transactions and do not block the forwarding path.

The operator page's `CONTEXT` view uses the Control RPCs
`ListFlowContextTags` and `ListFlowContextActions`; it never sends a backend
identity token to `/flow-context/rpc`. `ListFlowContextTags` accepts `query`,
`scope` (`all`, `flow`, or `client`), `limit`, and `offset`, and returns unique
unexpired Tag projections. `ListFlowContextActions` accepts `query`, `limit`,
and `offset`, and returns recent set/unset events with actor, Flow ID, and
client IP when available.

The same RPC endpoint exposes direct controls for the currently active Flow:

```json
{"method":"SetFlowLimit","params":{"flow_id":"01...","rate_bps":1000000}}
{"method":"ClearFlowLimit","params":{"flow_id":"01..."}}
{"method":"BlockFlow","params":{"flow_id":"01...","reason":"abuse"}}
```

`rate_bps` is a bidirectional Flow budget. A backend limit can only reduce an
existing persistent or online limit; `ClearFlowLimit` restores that policy
limit. `BlockFlow` closes the current TCP stream or UDP mapping with the
`backend_blocked` close reason. Neither operation affects future Flows; use an
online rule when future connections must be denied or limited. These controls
require an active Flow and the identity's route/upstream permission. Invalid
parameters return `400`, an unauthorized Flow returns `403`, a closed or
otherwise inactive Flow returns `409`, and an unavailable controller or audit
store returns `503`. Control application is exactly-once at the data plane;
repeating `BlockFlow`, or racing a control request with Flow close, returns
`409` when the operation was not applied.

The Go package `pkg/flowcontextclient` provides `Client` for one instance and
`ClientSet` for multiple fbforward instances. Each instance is identified by
the unique source address visible to the backend, so callers do not construct
tuple JSON manually.

For a UDP backend, read the source address of the packet received from
fbforward and use the actual address of the backend socket as the destination:

```go
source := netip.MustParseAddrPort("10.0.0.2:53000")
destination := netip.MustParseAddrPort("10.0.0.20:443")
flow, err := clients.ResolveBackendTuple(ctx, "udp", source, destination)
if err != nil {
	return err
}
return flow.SetFlowTag(ctx, flowcontextclient.Tag{
	Namespace: "app",
	Key:       "user",
	Value:     userID,
})
```

Each fbforward must present a unique source IP to the backend. If multiple
instances share one SNAT source IP, `ClientSet` cannot select the originating
instance. The `backend_key` comes from the selected instance configuration and
must exactly match the backend key recorded by fbforward; the destination only
identifies the backend tuple and does not select or override that key. Use the
actual backend address rather than a wildcard listener address, or obtain the
destination with packet-info support. `HasSource` is only local traffic
classification and is not authentication. Keep Flow Context on a trusted
network or behind external TLS. TCP callers can continue using `ResolveConn`.

## Security and limits

- Tokens are sent only in the `Authorization` header.
- Tokens are never accepted from query strings or stored in URLs.
- Request bodies have endpoint-specific hard limits.
- Control and Flow Context identities are separate.
- The API does not implement arbitrary SQL, PROXY protocol, or TProxy.

The static page is not an authentication boundary: protect the HTTP listener
at the network layer and rely on the API token for data access. Prometheus
exposes operational measurements but does not provide a second control path.
Flow Context backend tokens cannot call control RPCs, and the control token
cannot resolve or tag backend Flows.
