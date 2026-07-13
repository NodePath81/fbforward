# Backend Flow Context Client

`pkg/flowcontextclient` is a small synchronous Go client for backend
applications. It is not part of fbforward's data plane: fbforward exposes the
Flow Context HTTP endpoints, while the backend uses this package to query the
Flow associated with an accepted socket.

The package intentionally has no cache, retry loop, queue, goroutine, service
discovery, or sidecar. A shared `http.Client` is sufficient for the expected
low connection rate. The default request timeout is 500 ms and the default
tuple wait is 100 ms.

For one instance, configure `Client` with the endpoint, backend identity token,
and the exact `backend_key` used by fbforward. Call `ResolveConn` once per TCP
connection. It converts:

```text
backend conn.RemoteAddr() → fbforward request local_addr
backend conn.LocalAddr()  → fbforward request remote_addr
```

For several instances, configure a `ClientSet`. `SourceAddr` is the unique
fbforward source address visible to the backend. Selection is an exact linear
match on that IP; source ports are ignored. The set never broadcasts a
request, and an unknown source returns `ErrUnknownInstance` so direct or
unconfigured traffic can be handled separately.

After a Flow's grace period ends, its tuple is indistinguishable from an
unknown tuple and returns `ErrFlowNotFound`.

`ResolvedFlow` records the selected instance. Its tag methods always write to
the same instance that resolved the flow, avoiding a second lookup. The
backend may use `Flow.ClientAddr` for logging or application policy, but the
client does not expose a proxy protocol or inject headers into application
traffic.

The Flow Context token is separate from the ControlServer token. Remote
deployment must protect the HTTP endpoint with TLS or a trusted private
network; the client does not add TLS termination or credential discovery.
