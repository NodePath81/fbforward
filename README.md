# fbforward

fbforward is a Linux TCP/UDP port forwarder. Each listener points to a
route; each new Flow is assigned one upstream and remains pinned to it until
close. Adaptive routes use local health and RTT observations from `fbmeasure`;
static routes use their configured upstream.

The service also provides an authenticated HTTP control API, Prometheus
metrics, local SQLite audit storage, firewall policy reload, and Flow Context
lookup for backend applications. It does not implement transparent socket
identity propagation, kernel traffic control, distributed state, or arbitrary
SQL.

## Build and run

Requirements: Linux, Go 1.25.5 or newer, and a C toolchain for SQLite.

```bash
make build
cp configs/config.example.yaml config.yaml
./build/bin/fbforward --config config.yaml
```

The build produces `fbforward` and `fbmeasure`. Run `fbmeasure` on upstream
hosts that are used by adaptive routes; its deployment is independent of the
forwarder's control plane.

## Minimal topology

```yaml
listeners:
  - name: web
    bind: 0.0.0.0:9000
    protocol: tcp
    route: web
routes:
  - name: web
    strategy: static
    upstreams: [local]
upstreams:
  - tag: local
    destination: {host: 127.0.0.1}
```

Use [configs/config.example.yaml](/home/huangyj/Workspace/fbforward/configs/config.example.yaml)
for the complete current sample. The YAML decoder is strict; removed or
unknown fields fail startup.

## Documentation

- [Getting started](doc/getting-started.md)
- [Configuration](doc/configuration.md)
- [Operations](doc/operations.md)
- [API](doc/api.md)
- [Architecture](doc/architecture.md)
- [Development and testing](doc/development.md)

Historical design notes and test baselines are kept under `doc/archive/`.
