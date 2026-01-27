# Getting started

This document guides you through installing fbforward and running your first deployment.

---

## 2.1 Prerequisites

### Platform requirements

fbforward requires Linux. The forwarder uses platform-specific kernel features that are not available on other operating systems:

- `SO_MAX_PACING_RATE`: Socket option for rate-limiting TCP sends (used by bwprobe)
- `TCP_INFO`: Socket option for reading TCP connection statistics
- Raw ICMP sockets: For sending and receiving ICMP echo requests

Tested distributions include Ubuntu 22.04+, Debian 12+, and Fedora 38+. Kernel version 5.10 or newer is recommended.

### Go toolchain

Building from source requires Go 1.25.5 or newer. Verify your Go version:

```bash
go version
# Expected output: go version go1.25.5 linux/amd64 (or newer)
```

If Go is not installed or the version is older than 1.25.5, download from [golang.org](https://golang.org/dl/).

### Linux capabilities

fbforward requires the following capabilities:

**CAP_NET_RAW** (required): Allows sending raw ICMP packets for reachability probing. Without this capability, fbforward cannot start.

**CAP_NET_ADMIN** (optional): Allows configuring traffic control qdiscs for bandwidth shaping. Only required if `shaping.enabled: true` in configuration.

Capabilities can be assigned to the binary or granted via systemd's `AmbientCapabilities` directive.

### Node.js and npm

Building the web UI requires Node.js 18+ and npm. Check versions:

```bash
node --version  # Expected: v18.0.0 or newer
npm --version   # Expected: 9.0.0 or newer
```

If Node.js is not installed, download from [nodejs.org](https://nodejs.org/).

The web UI is optional. If Node.js is unavailable, the build process skips UI compilation and uses any existing `web/dist` directory.

### fbmeasure on upstream hosts

fbforward requires fbmeasure running on each upstream host to perform bandwidth measurements. Without fbmeasure, fbforward operates in degraded mode using ICMP probes only.

Deploy fbmeasure on upstreams before starting fbforward. See [Section 3.3](user-guide-fbmeasure.md) for fbmeasure setup.

---

## 2.2 Installation

### Building from source

Clone the repository:

```bash
git clone https://github.com/NodePath81/fbforward.git
cd fbforward
```

Build all binaries:

```bash
make build
```

This command:
1. Builds the web UI (if Node.js available) and places output in `web/dist`
2. Builds fbforward binary to `build/bin/fbforward`
3. Builds bwprobe binary to `build/bin/bwprobe`
4. Builds fbmeasure binary to `build/bin/fbmeasure`

To build individual binaries:

```bash
make build-fbforward  # fbforward only (includes UI build step)
make build-bwprobe    # bwprobe only
make build-fbmeasure  # fbmeasure only
```

To build without make:

```bash
# Build UI (optional)
cd web && npm install && npm run build && cd ..

# Build fbforward
go build -o build/bin/fbforward ./cmd/fbforward

# Build bwprobe
go build -o build/bin/bwprobe ./bwprobe/cmd

# Build fbmeasure
go build -o build/bin/fbmeasure ./bwprobe/cmd/fbmeasure
```

### Debian package installation

Build a Debian package:

```bash
deploy/packaging/debian/build.sh
```

This script creates a `.deb` file in `deploy/packaging/debian/build/`. The package includes:

- Binary installed to `/usr/local/bin/fbforward`
- systemd service file at `/etc/systemd/system/fbforward.service`
- Default configuration directory at `/etc/fbforward/`

Install the package:

```bash
sudo dpkg -i deploy/packaging/debian/build/fbforward_*.deb
```

The package creates a `fbforward` user and group. The systemd service runs as this unprivileged user with ambient capabilities.

### Setting capabilities manually

If not using systemd or the Debian package, assign capabilities to the binary:

```bash
# Required: CAP_NET_RAW for ICMP
sudo setcap cap_net_raw+ep ./build/bin/fbforward

# Optional: Add CAP_NET_ADMIN for traffic shaping
sudo setcap cap_net_raw,cap_net_admin+ep ./build/bin/fbforward
```

Verify capabilities:

```bash
getcap ./build/bin/fbforward
# Expected output: ./build/bin/fbforward cap_net_raw=ep
```

### systemd service setup

Copy the service file:

```bash
sudo cp deploy/systemd/fbforward.service /etc/systemd/system/
sudo systemctl daemon-reload
```

The service file grants `CAP_NET_RAW`, `CAP_NET_BIND_SERVICE`, and `CAP_NET_ADMIN` via `AmbientCapabilities`. The service runs as user `fbforward`.

Create the user if not using the Debian package:

```bash
sudo useradd -r -s /bin/false fbforward
```

Create the configuration directory:

```bash
sudo mkdir -p /etc/fbforward
sudo chown fbforward:fbforward /etc/fbforward
```

Place your configuration file at `/etc/fbforward/config.yaml`.

Enable and start the service:

```bash
sudo systemctl enable fbforward
sudo systemctl start fbforward
```

Check service status:

```bash
sudo systemctl status fbforward
```

View logs:

```bash
sudo journalctl -u fbforward -f
```

---

## 2.3 Quick start

This section walks through a minimal deployment with two upstreams.

### Step 1: Deploy fbmeasure on upstreams

On each upstream host (example: `upstream1.example.com` and `upstream2.example.com`), start fbmeasure:

```bash
# Copy fbmeasure binary to upstream hosts
scp build/bin/fbmeasure user@upstream1.example.com:/usr/local/bin/
scp build/bin/fbmeasure user@upstream2.example.com:/usr/local/bin/

# On each upstream, run fbmeasure
ssh user@upstream1.example.com 'fbmeasure --port 9876 &'
ssh user@upstream2.example.com 'fbmeasure --port 9876 &'
```

fbmeasure listens on the specified port and accepts bwprobe connections. Ensure firewall rules allow TCP connections to port 9876 from the fbforward host.

Verify connectivity from the fbforward host:

```bash
./build/bin/bwprobe -server upstream1.example.com:9876 -bandwidth 10m -samples 3
./build/bin/bwprobe -server upstream2.example.com:9876 -bandwidth 10m -samples 3
```

Both commands should complete without errors.

### Step 2: Create minimal configuration

Create `config.yaml` with the following content:

```yaml
forwarding:
  listeners:
    - bind_addr: 0.0.0.0
      bind_port: 9000
      protocol: tcp
    - bind_addr: 0.0.0.0
      bind_port: 9000
      protocol: udp

upstreams:
  - tag: primary
    destination:
      host: upstream1.example.com
    measurement:
      host: upstream1.example.com
      port: 9876
  - tag: backup
    destination:
      host: upstream2.example.com
    measurement:
      host: upstream2.example.com
      port: 9876

control:
  bind_addr: 127.0.0.1
  bind_port: 8080
  auth_token: "change-me-to-random-string"
  webui:
    enabled: true
  metrics:
    enabled: true
```

This configuration:
- Listens for TCP and UDP traffic on port 9000
- Forwards to two upstreams: `upstream1.example.com` (tag: `primary`) and `upstream2.example.com` (tag: `backup`)
- Enables web UI and Prometheus metrics on `127.0.0.1:8080`
- Requires Bearer token `change-me-to-random-string` for API access

Replace `upstream1.example.com` and `upstream2.example.com` with your actual upstream hostnames or IP addresses. Replace `change-me-to-random-string` with a randomly generated token.

### Step 3: Validate configuration

Validate the configuration file:

```bash
./build/bin/fbforward check config.yaml
# Expected output: config valid: 2 upstreams, 2 listeners
```

If validation fails, the command prints error details and exits with status 1.

### Step 4: Start fbforward

Start fbforward with the configuration:

```bash
./build/bin/fbforward --config config.yaml
```

Expected log output:

```
2025/01/26 12:00:00 INFO config loaded path=config.yaml upstreams=2 listeners=2
2025/01/26 12:00:00 INFO resolved upstream tag=primary host=upstream1.example.com ip=203.0.113.10
2025/01/26 12:00:00 INFO resolved upstream tag=backup host=upstream2.example.com ip=203.0.113.11
2025/01/26 12:00:00 INFO starting ICMP prober
2025/01/26 12:00:00 INFO starting bwprobe collector
2025/01/26 12:00:00 INFO fast-start mode enabled timeout=30s
2025/01/26 12:00:00 INFO listening addr=0.0.0.0:9000 protocol=tcp
2025/01/26 12:00:00 INFO listening addr=0.0.0.0:9000 protocol=udp
2025/01/26 12:00:00 INFO control server started addr=127.0.0.1:8080
2025/01/26 12:00:05 INFO primary selected tag=primary score=0.85 mode=fast-start
```

The `primary selected` line confirms fbforward has chosen an upstream. In fast-start mode, selection uses lightweight TCP dial probes to each measurement endpoint until full bwprobe measurements complete.

### Step 5: Verify operation

Open the web UI in a browser:

```
http://127.0.0.1:8080/
```

The UI displays:
- Current primary upstream
- Per-upstream metrics (bandwidth, RTT, jitter, loss/retransmit rates)
- Active flow counts
- Score history chart

Test TCP forwarding:

```bash
# From a client machine
curl http://<fbforward-host>:9000/
```

The request is forwarded to the primary upstream.

Test UDP forwarding:

```bash
# From a client machine
echo "test" | nc -u <fbforward-host> 9000
```

Check Prometheus metrics:

```bash
curl -H "Authorization: Bearer change-me-to-random-string" http://127.0.0.1:8080/metrics
```

Metrics include `fbforward_flows_active`, `fbforward_upstream_score`, `fbforward_bytes_sent_total`, and others.

### Step 6: Monitor switching behavior

fbforward selects the primary upstream automatically based on measured quality. To observe switching:

1. Watch the web UI or stream status via WebSocket:

```bash
# Use wscat or similar WebSocket client
wscat -c ws://127.0.0.1:8080/status -H "Authorization: Bearer change-me-to-random-string"
```

2. Degrade network quality on the current primary (e.g., add latency with tc or disconnect the link)

3. After the confirmation duration elapses (default 60s), fbforward switches to the backup upstream

4. New flows go to the new primary; existing flows remain pinned to their original upstream

### Next steps

- Read [Section 3.1](user-guide-fbforward.md) for detailed fbforward operation
- Read [Section 4](configuration-reference.md) for complete configuration options
- Read [Section 6.1](algorithm-upstream-selection.md) for upstream selection algorithm details
