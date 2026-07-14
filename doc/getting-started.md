# Getting started

This guide runs a local fbforward instance with the current explicit topology.

## Requirements

- Linux;
- Go 1.25.5 or newer;
- gcc or another working C toolchain for SQLite;
- `fbmeasure` on upstream hosts used by adaptive routes.

## Build

```bash
make build
```

To build only one binary:

```bash
make build-fbforward
make build-fbmeasure
```

## Configure

```bash
cp configs/config.example.yaml config.yaml
```

Edit at least the listener bind address, route, upstream destination, and a
random control token of at least 16 characters. The active topology is:

```text
listener -> route -> upstream list
```

See [configuration](configuration.md) for field meanings. The sample file is
the canonical complete configuration fixture.

## Validate and start

```bash
./build/bin/fbforward check --config config.yaml
./build/bin/fbforward --config config.yaml
```

The legacy `--config` invocation starts the forwarder directly. Unknown YAML
fields and removed features are rejected before listeners start.

## Verify forwarding

Start a TCP or UDP service on the configured upstream, then connect to the
listener with a client tool such as `nc` or `iperf3`. Confirm the Flow in the
authenticated `GetActiveFlows` RPC and inspect closed records through
`QueryAudit`.

For an adaptive route, verify that the measurement endpoint is reachable from
the fbforward host and that the first probe runs immediately. Static routes do
not require a measurement scheduler.

## Next steps

- [Configuration reference](configuration.md)
- [Operations and troubleshooting](operations.md)
- [Control and Flow Context APIs](api.md)
- [Runtime architecture](architecture.md)
- [Development and testing](development.md)
