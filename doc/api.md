# API reference

The ControlServer uses HTTP. Control and operator endpoints require the
configured bearer token. RPC requests are `POST /rpc` with `{method, params}`;
responses keep the `{ok, result, error}` shape. Errors are JSON and requests
are authenticated, rate-limited, size-limited, and audited.

## Runtime and routes

Read-only methods include `GetStatus`, `GetActiveFlows`, `GetRouteStatus`,
`ListUpstreams`, `GetRuntimeConfig`, `GetMeasurementConfig`,
`GetScheduleStatus`, and `GetIPLogStatus`.

Route-local control methods are:

- `SetRouteOverride` with `{route, upstream}`;
- `ClearRouteOverride` with `{route}`;
- `RunMeasurement` with `{tag, protocol}`;
- `Restart` and `SendTestNotification`.

Overrides affect new Flows. Adaptive routes fall back within their configured
route when an override is unavailable; static routes do not automatically
fail over. `SetUpstream` remains only as a deprecated compatibility wrapper
for a single-route configuration and accepts `auto` or `manual`.

## Health, GeoIP, and metrics

- `GetGeoIPStatus` returns local database state.
- `ReloadGeoIP` reopens configured local MMDB files; it never downloads data.
- `GET /metrics` returns Prometheus metrics when enabled.
- `GET /identity` returns instance identity for the operator page.

Measurement returns health and raw RTT. TCP and UDP observations contribute to
one upstream health snapshot. Static-only routes do not start a scheduler.

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

## Firewall and online rules

Persistent policy methods are `GetFirewallPolicy`, `GetFirewallStatus`,
`ValidateFirewallPolicy`, and `ReloadFirewallPolicy`. Reload compiles and
atomically swaps the policy; an invalid candidate leaves the current policy
active.

Online-rule methods are `CreateOnlineRule`, `ListOnlineRules`,
`DeleteOnlineRule`, and `ExpireOnlineRule`. Rules have bounded TTL and are
stored separately from the persistent policy. Create, delete, and expire
events are audited. Online deny cannot be bypassed by a non-deny action.

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
`SetClientTag`, `UnsetClientTag`, and `ListFlowTags`. Backend identities have
exact route, upstream, and namespace permissions. Tag changes are persisted in
SQLite transactions and do not block the forwarding path.

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
