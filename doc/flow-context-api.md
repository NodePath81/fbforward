# Flow Context API

The tag-capable Flow Context API is available on a Unix domain socket. It is
disabled unless `flow_context.enabled` is explicitly set to `true`.

```yaml
flow_context:
  enabled: true
  socket_path: /run/fbforward/flow-context.sock
  auth_token: "a-separate-long-random-token"
  allowed_namespaces: [app, tenant]
  max_ttl: 24h
```

The socket is created with mode `0660`. Requests use HTTP JSON over the Unix
socket and must contain `Authorization: Bearer <flow_context.auth_token>`.
The RPC endpoint is `POST /v1/rpc` with one of these method names:

- `ResolveFlow`
- `SetFlowTag`
- `UnsetFlowTag`
- `SetClientTag`
- `UnsetClientTag`
- `ListFlowTags`

Tag writes include `flow_id`, an `identity` object (`backend_key`, `route`,
and `upstream`), and `namespace`/`key`/`value` fields. A backend can only read
or label flows whose route, upstream, and backend key exactly match its
identity. Tag values use the canonical form `namespace:key=value`, and a new
value replaces the previous value for the same namespace/key pair.

All successful writes append a `flow_tag_events` audit row and update the
current tag projection in SQLite. Expired tags are excluded from queries.
