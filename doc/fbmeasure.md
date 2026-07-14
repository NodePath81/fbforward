# fbmeasure

`fbmeasure` is a small TCP/UDP echo service used to answer one question:
is an upstream reachable, and what is its approximate RTT?

It is not a bandwidth tester, score calculator, generic public echo service, or
authentication service.

## Deployment boundary

The protocol intentionally has no TLS, token, or challenge layer. Run it on a
trusted private network, WireGuard link, or a host protected by a firewall that
allows only the configured fbforward instances. The default listener is
`127.0.0.1:9876`; remote deployment must explicitly select a private address.

Do not expose an unauthenticated UDP echo service to the public Internet. TCP
and UDP responses are fixed-size, but an open UDP listener can still be used
for reflection or resource exhaustion.

## Running the server

```bash
fbmeasure --listen 10.0.0.2:9876 --log-format json
```

The same numeric port serves both TCP and UDP. The server accepts only fixed
32-byte frames, three frames per TCP connection, and three samples per client
measurement. It applies bounded TCP concurrency and a fixed UDP packet rate.

## Measurement semantics

Each measurement sends three small probes. A valid response to at least one
probe means reachable. RTT is the minimum successful sample. No mean, max,
jitter, loss percentage, bandwidth, or score is produced.

TCP uses a new short connection for each measurement. UDP uses a connected UDP
socket and verifies the frame, sequence, nonce, and response size. The service
does not calculate timestamps; the client measures elapsed time locally.

## Go SDK

The public package is `github.com/NodePath81/fbforward/pkg/fbmeasure`:

```go
client, err := fbmeasure.NewClient(fbmeasure.ClientConfig{
    Address: "10.0.0.2:9876",
    Timeout: 2 * time.Second,
})
if err != nil {
    return err
}
defer client.Close()

result, err := client.ProbeTCP(ctx)
```

`ProbeUDP` has the same result shape. `Result` contains only protocol,
reachable, RTT, and observation time. The client does not maintain a pool,
retry queue, cache, or persistent connection.

Programs that need to embed the responder can use `NewServer`, `Serve`,
`Addr`, and `Close` from the same package. The standalone binary remains the
recommended deployment because it keeps the measurement listener separate
from application lifecycle and firewall policy.

## fbforward integration

Only adaptive routes start measurement. The collector converts each SDK result
into a TCP or UDP observation and updates the shared upstream HealthSnapshot.
Health thresholds, stale state, route-local selection, and Flow pinning remain
fbforward responsibilities. Static routes do not require fbmeasure.

The protocol is versioned and this refactor is a breaking change: client and
server must be upgraded together. The former JSON control protocol and TLS
flags are not supported.
