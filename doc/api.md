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

Read-only methods include `GetStatus`, `GetActiveFlows`, `GetRouteStatus`,
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
rejections protocol=tcp reason="connection limit" | limit 100
```

Sources are `flows`, `rejections`, `events`, `top clients`, and `top asns`.
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

Tag RPC methods are `ResolveFlow`, `SetFlowTag`, `UnsetFlowTag`,
`SetClientTag`, `UnsetClientTag`, and `ListFlowTags`. Tag writes contain a
`flow_id` or resolved client, `namespace`, `key`, `value`, and optional
`ttl_seconds`; namespaces and TTL are checked against the authenticated
identity and configured maximum. Backend identities have exact route,
upstream, and namespace permissions. Tag changes are persisted in SQLite
transactions and do not block the forwarding path.

The Go package `pkg/flowcontextclient` provides `Client` for one instance and
`ClientSet` for multiple fbforward instances. Each instance is identified by
the unique source address visible to the backend, so callers do not construct
tuple JSON manually.

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
