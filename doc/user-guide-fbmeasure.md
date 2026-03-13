# fbmeasure user guide

This guide covers fbmeasure deployment, configuration, and operation.

---

## 3.3.1 Overview

### Purpose

fbmeasure is the measurement server binary that runs on upstream hosts.
fbforward connects to it to collect:

- TCP RTT and jitter
- UDP RTT and jitter
- TCP retransmission rate
- UDP loss rate

Without fbmeasure, fbforward operates in degraded mode using ICMP reachability
probes only.

### Protocol model

fbmeasure exposes a single TCP control listener and a UDP listener on the same
configured port (default `9876`).

- TCP control requests use a 4-byte length-prefixed JSON protocol.
- UDP probes use compact binary packets tagged with a one-shot `test_id`.
- TCP retransmission tests use a client-initiated data connection carrying a
  binary preface plus the same `test_id`.

The server supports four operations:

- `ping_tcp`
- `ping_udp`
- `tcp_retrans`
- `udp_loss`

The protocol is stateless apart from short-lived pending tests keyed by
`test_id`.

### Relationship to fbforward

fbforward uses `upstreams[].measurement.host` and
`upstreams[].measurement.port` to reach fbmeasure. Each measurement cycle runs
one TCP probe job and one UDP probe job per upstream when those protocols are
enabled.

### Platform requirements

fbmeasure requires Linux. TCP retransmission measurement depends on
`TCP_INFO`, and the server uses Linux socket behavior throughout the probing
path.

No special capabilities are required. fbmeasure can run as an unprivileged
user.

---

## 3.3.2 Configuration

fbmeasure uses CLI flags only. It does not read a configuration file.

### CLI flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--port` | int | `9876` | TCP and UDP listen port |
| `--log-level` | string | `info` | `debug`, `info`, `warn`, or `error` |
| `--log-format` | string | `text` | `text` or `json` |
| `--recv-wait` | duration | `100ms` | UDP receive window after loss-test send phase |
| `--version` | bool | `false` | Print version and exit |

Example:

```bash
./fbmeasure --port 9876 --log-format json
```

### Firewall requirements

fbmeasure requires inbound TCP and UDP access on the configured port from the
fbforward host.

```bash
# TCP control + TCP retransmission data connection
sudo ufw allow from <fbforward-host-ip> to any port 9876 proto tcp

# UDP ping/loss probes
sudo ufw allow from <fbforward-host-ip> to any port 9876 proto udp
```

### fbforward configuration example

```yaml
upstreams:
  - tag: primary
    destination:
      host: upstream1.example.com
    measurement:
      host: upstream1.example.com
      port: 9876
```

---

## 3.3.3 Operation

### Repository deployment artifacts

The repository ships a container deployment artifact for fbmeasure:

- `deploy/container/fbmeasure/Containerfile`

### Running in foreground

```bash
./fbmeasure --port 9876
```

The server logs startup, shutdown, and failed operations through `slog`. In
JSON mode the `component` field is `fbmeasure`.

### Running in a container

Build the supplied image from the repository root:

```bash
podman build -f deploy/container/fbmeasure/Containerfile -t fbmeasure:latest .
```

Run it with both TCP and UDP ports published:

```bash
podman run -d --name fbmeasure \
  --restart unless-stopped \
  -p 9876:9876/tcp \
  -p 9876:9876/udp \
  fbmeasure:latest
```

Docker can use the same `Containerfile` with equivalent commands.

### Verification

Basic reachability:

```bash
nc -zv <upstream-host> 9876
```

End-to-end verification from the fbforward host is usually easiest by checking
that fbforward measurements begin succeeding and stale-measurement warnings
stop appearing.

---

## 3.3.4 Troubleshooting

### fbmeasure fails to start with "address already in use"

Another process is already bound to the configured port.

```bash
ss -ltnup | grep 9876
./fbmeasure --port 9877
```

If you change the port, update `upstreams[].measurement.port` in fbforward.

### fbforward reports measurement failures

Check:

1. fbmeasure is running.
2. TCP and UDP firewall rules allow the fbforward host.
3. `measurement.host` and `measurement.port` match the deployed service.
4. The upstream is Linux if TCP retransmission tests are enabled.

If running in a container, inspect:

```bash
podman logs -f fbmeasure
```

### UDP loss tests need a longer receive window

Increase `--recv-wait` if late UDP packets are being truncated on very high
latency paths:

```bash
./fbmeasure --port 9876 --recv-wait 250ms
```
