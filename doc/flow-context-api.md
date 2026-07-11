# Flow Context API

Flow Context is exposed through the ControlServer TCP HTTP listener and is
disabled unless `flow_context.enabled` is explicitly enabled. Remote callers
must use a TLS reverse proxy or a trusted private network; fbforward does not
terminate TLS in this API.

```yaml
flow_context:
  enabled: true
  max_ttl: 24h
  identities:
    - id: caddy
      token: "a-long-random-backend-token"
      routes: [web]
      upstreams: [primary]
      namespaces: [app, tenant]
```

Each identity has its own bearer token and exact route, upstream, and tag
namespace allowlists. The request body never supplies identity, route,
upstream, or actor values; those are derived from the authenticated token.
The control-plane token is not accepted by these endpoints.

## Resolve

`POST /flow-context/resolve` accepts the backend tuple:

```json
{
  "protocol": "tcp",
  "backend_key": "primary@192.0.2.10:443",
  "local_addr": "10.0.0.1:43122",
  "remote_addr": "192.0.2.10:443",
  "wait_ms": 1000
}
```

The response keeps the existing `{ "ok": true, "flow": { ... } }` shape.
Closed Flows remain resolvable only during the Registry grace period.

## Tags

`POST /flow-context/rpc` uses the following JSON envelope:

```json
{
  "method": "SetFlowTag",
  "params": {
    "flow_id": "...",
    "namespace": "app",
    "key": "owner",
    "value": "caddy",
    "ttl_seconds": 3600
  }
}
```

Supported methods are `ResolveFlow`, `SetFlowTag`, `UnsetFlowTag`,
`SetClientTag`, `UnsetClientTag`, and `ListFlowTags`. Tag values use the
canonical form `namespace:key=value`; setting a key replaces its previous
value. TTL is bounded by `flow_context.max_ttl`.

Every write appends a `flow_tag_events` audit row and updates the current
SQLite tag state in one transaction. The active Flow identity is stored in
`flow_entities`; `flows` is written only once the complete lifecycle closes.
Expired tags are excluded from queries and removed by retention cleanup.
