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

## Backend Go client

Backend code can use the small `pkg/flowcontextclient` package instead of
building tuple JSON by hand. For one fbforward instance:

```go
client, err := flowcontextclient.New(flowcontextclient.Options{
    Endpoint:   "http://127.0.0.1:8080",
    Token:      os.Getenv("FLOW_CONTEXT_TOKEN"),
    BackendKey: "primary@192.0.2.10:443",
})
flow, err := client.ResolveConn(ctx, backendConn)
```

`ResolveConn` reverses the backend socket direction automatically. The
returned `Flow.ClientAddr` is the original client address; it is informational
and does not require the backend to handle that address. Tag writes can use
`SetFlowTag`, `SetClientTag`, `UnsetFlowTag`, and `UnsetClientTag`.

When a backend accepts connections from more than one fbforward instance, use
`ClientSet`. Each instance must have a unique source address visible in
`backendConn.RemoteAddr()`:

```go
clients, err := flowcontextclient.NewClientSet([]flowcontextclient.InstanceOptions{
    {
        Name:       "edge-a",
        SourceAddr: netip.MustParseAddr("10.10.1.12"),
        Client: flowcontextclient.Options{
            Endpoint: "http://10.10.1.12:8080", Token: edgeAToken,
            BackendKey: "primary@192.0.2.10:443",
        },
    },
    {
        Name:       "edge-b",
        SourceAddr: netip.MustParseAddr("10.10.2.12"),
        Client: flowcontextclient.Options{
            Endpoint: "http://10.10.2.12:8080", Token: edgeBToken,
            BackendKey: "primary@192.0.2.10:443",
        },
    },
})
resolved, err := clients.ResolveConn(ctx, backendConn)
if err == nil {
    _ = resolved.SetFlowTag(ctx, flowcontextclient.Tag{
        Namespace: "app", Key: "user", Value: "user-123",
    })
}
```

An unknown source address returns `ErrUnknownInstance` without querying any
endpoint. The client does not retry another instance after a selected
instance returns an error. The optional `ClientSet.ConnContext` helper can
attach a resolved Flow to a standard `net/http.Server` connection context;
applications that need to distinguish failure causes should call
`ResolveConn` directly.
