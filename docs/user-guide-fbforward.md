# fbforward user guide

This guide covers fbforward operation, configuration, and troubleshooting.

---

## 3.1.1 Overview

### What fbforward does

fbforward is a TCP/UDP port [forwarder](glossary.md#forwarder) that selects the best [upstream](glossary.md#upstream) based on measured network quality. The forwarder accepts client connections on configured [listeners](glossary.md#listener), proxies traffic to a selected upstream, and continuously measures upstream quality using bwprobe bandwidth tests and ICMP reachability probes.

### NAT-style forwarding model

fbforward acts as a network address translation (NAT) proxy:

- Clients connect to fbforward's listen address
- fbforward establishes a separate connection to the upstream
- The upstream sees fbforward as the source address, not the original client
- Response traffic flows back through fbforward to the client

This differs from transparent proxying, where the upstream would see the original client address.

### Flow pinning semantics

Once a [flow](glossary.md#flow) (TCP connection or UDP 5-tuple mapping) is assigned to an upstream, it remains [pinned](glossary.md#flow-pinning) to that upstream until completion:

**TCP flow lifecycle**:
1. Client connects to listener
2. fbforward checks [flow table](glossary.md#flow-table)
3. If no entry exists, create entry pinned to current [primary upstream](glossary.md#primary-upstream)
4. Establish connection to pinned upstream
5. Bidirectionally copy data until FIN/RST or idle timeout
6. Remove flow table entry

**UDP flow lifecycle**:
1. Client sends packet to listener
2. fbforward checks flow table using 5-tuple key (protocol, src IP, src port, dst IP, dst port)
3. If no entry exists, create entry pinned to current primary upstream and allocate dedicated socket pair
4. Forward packets bidirectionally until idle timeout expires
5. Remove flow table entry and close sockets

### Operational modes

fbforward supports two upstream selection modes:

**Auto mode** (default): The [scoring engine](glossary.md#scoring-engine) evaluates upstream quality using bwprobe measurements. When a candidate upstream's score exceeds the current primary's score by the configured [threshold](glossary.md#score-delta-threshold) for the [confirmation duration](glossary.md#confirm-duration), fbforward switches to the new primary. Switching requires the [hold time](glossary.md#hold-time) to have elapsed since the last switch.

**Manual mode**: An operator selects an upstream via the control plane RPC method `SetUpstream`. fbforward validates the upstream is usable (not marked [unusable](glossary.md#unusable-upstream)) before accepting the selection. The system remains on the selected upstream until another manual selection occurs.

Mode is determined by configuration:
- Auto mode active when `switching.auto` section is present
- Manual mode active when operator calls `SetUpstream` RPC

### Fast failover

fbforward triggers immediate upstream switching on severe quality degradation:

- **High loss/retransmit rate**: When TCP retransmit rate or UDP loss rate exceeds configured thresholds (default 20%) over recent measurement windows
- **Dial failures**: When consecutive TCP dial attempts to an upstream fail

Fast failover bypasses normal confirmation duration requirements.

### Unusable upstream recovery

An upstream becomes unusable when:
- 100% packet loss detected over probe window
- Consecutive TCP dial failures exceed threshold
- Measurement server connection fails repeatedly

Unusable upstreams are excluded from selection. When probes succeed again, the upstream automatically returns to usable state and becomes eligible for selection.

### Command-line interface

fbforward provides the following commands:

| Command | Description |
|---------|-------------|
| `fbforward run --config <path>` | Start the forwarder with specified configuration |
| `fbforward check --config <path>` | Validate configuration file without starting |
| `fbforward version` | Print version and exit |
| `fbforward help` | Show usage information |

**Legacy invocation forms** (for backward compatibility):

```bash
fbforward --config config.yaml    # Legacy flag form
fbforward config.yaml             # Positional argument form
```

These legacy forms are equivalent to `fbforward run --config config.yaml`.

**Config validation:**

The `check` command parses and validates the configuration file, reporting the number of upstreams and listeners on success:

```bash
$ fbforward check --config config.yaml
config valid: 2 upstreams, 1 listeners

$ fbforward check --config invalid.yaml
config invalid: field 'upstreams' is required
```

Use `check` before deploying configuration changes to catch syntax and schema errors.

---

## 3.1.2 Configuration

fbforward loads configuration from a YAML file specified via `--config` flag. The configuration defines listeners, upstreams, measurement parameters, scoring weights, switching policy, and control plane settings.

### Configuration file format

Configuration uses YAML with custom unmarshaling for duration and bandwidth values:

**Duration format**: Number followed by unit suffix. Valid suffixes: `s` (seconds), `m` (minutes), `h` (hours). Examples: `30s`, `5m`, `1h`.

**Bandwidth format**: Number followed by unit suffix. Valid suffixes: `k` (Kbps), `m` (Mbps), `g` (Gbps). Examples: `10m` (10 Mbps), `1g` (1 Gbps).

Numbers without suffixes are interpreted as seconds (for durations) or bps (for bandwidth).

### Configuration structure

Configuration is organized into sections:

```yaml
hostname: "fbforward-host"          # Optional hostname override

forwarding:
  listeners: [...]                  # TCP/UDP bind addresses
  limits: {...}                     # Connection/mapping limits
  idle_timeout: {...}               # TCP/UDP idle timeouts

upstreams:                          # List of upstream definitions
  - tag: "primary"
    destination: {...}
    measurement: {...}
    priority: 0
    bias: 0
    shaping: {...}

dns:
  servers: [...]                    # Custom DNS resolvers
  strategy: "ipv4_only"             # DNS resolution strategy

reachability:
  probe_interval: 1s                # ICMP probe frequency
  window_size: 5                    # Probe window for reachability

measurement:
  startup_delay: 10s                # Delay before first measurement
  stale_threshold: 60m              # Max age for valid measurements
  fallback_to_icmp_on_stale: true   # Log warning when measurements stale
  schedule: {...}                   # Measurement scheduling
  fast_start: {...}                 # Fast-start mode config
  protocols: {...}                  # TCP/UDP test parameters

scoring:
  smoothing: {...}                  # EMA smoothing parameters
  reference: {...}                  # Target/ideal metric values
  weights: {...}                    # Metric importance weights
  utilization_penalty: {...}        # Utilization-based score reduction
  bias_transform: {...}             # Bias multiplier configuration

switching:
  auto: {...}                       # Auto mode switching parameters
  failover: {...}                   # Fast failover thresholds
  close_flows_on_failover: false    # Whether to close existing flows

control:
  bind_addr: "127.0.0.1"            # Control plane listen address
  bind_port: 8080                   # Control plane listen port
  auth_token: "..."                 # Bearer token for API auth
  webui: {...}                      # Web UI settings
  metrics: {...}                    # Prometheus metrics settings

shaping:
  enabled: false                    # Enable Linux tc traffic shaping
  interface: "eth0"                 # Physical interface
  ifb_device: "ifb0"                # IFB device for ingress
  aggregate_limit: "1g"             # Total bandwidth cap
```

See [Section 4](configuration-reference.md) for complete field documentation.

### Environment variable overrides

fbforward does not support environment variable overrides. All configuration must be specified in the YAML file.

### Validation rules

Configuration validation enforces:

- At least one upstream defined
- At least one listener defined
- Valid duration and bandwidth formats
- Positive probe intervals and timeouts
- Weight values sum to 1.0 per protocol
- Unique upstream tags
- Unique listener bind address/port/protocol combinations
- Referenced hostnames resolve via DNS (at startup)

Validation errors print to stderr and cause immediate exit with status 1.

Use `fbforward check --config <path>` to validate configuration without starting the forwarder.

---

## 3.1.3 Operation

### Starting and stopping

Start fbforward:

```bash
./fbforward --config config.yaml
```

The forwarder runs in the foreground and logs to stderr. Startup sequence:

1. Load and validate configuration
2. Resolve upstream hostnames via DNS
3. Create upstream manager with scoring configuration
4. Start ICMP reachability prober
5. Start bwprobe measurement collector
6. Start TCP/UDP listeners
7. Start control plane HTTP server
8. Enter running state

Expected startup logs:

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

Stop fbforward:

Send SIGINT (Ctrl+C) or SIGTERM:

```bash
kill -TERM <pid>
```

Shutdown sequence:

1. Stop accepting new connections (close listeners)
2. Wait for active TCP connections to close or timeout
3. Remove UDP mappings
4. Stop ICMP prober
5. Stop bwprobe collector
6. Shut down control plane
7. Exit

Graceful shutdown timeout is not configurable. Active TCP connections have up to the configured `idle_timeout.tcp` to complete.

### Hot reload via RPC

The control plane exposes a `Restart` RPC method to reload configuration without process restart:

```bash
curl -X POST http://127.0.0.1:8080/rpc \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "method": "Restart",
    "params": {},
    "id": 1
  }'
```

Restart sequence:

1. Load updated configuration from disk
2. Validate new configuration
3. Construct new Runtime with new config
4. Stop old Runtime (closes existing flows)
5. Start new Runtime
6. Return success response

**Warning**: Restart terminates all active flows. Clients must reconnect.

### Monitoring via web UI

Access the web UI at `http://<bind_addr>:<bind_port>/` (configured in `control` section).

**Authentication:**

The web UI requires a valid Bearer token (configured in `control.token`). On first access:

1. Navigate to `/auth` to enter your token
2. The UI validates the token by calling the `GetStatus` RPC method
3. On success, the token is stored in browser `localStorage` (key: `fbforward_token`)
4. You are redirected to the main UI

The token persists across browser sessions. To use a different token or rotate credentials:

1. Navigate to `/auth` directly
2. Enter the new token
3. The UI validates and saves the new token

**Security note:** Tokens are stored in browser localStorage, which is accessible to JavaScript running on the same origin. In production, always use HTTPS to protect token transmission.

The UI displays:

**Upstream status**:
- Current primary upstream (highlighted)
- Per-upstream scores and metrics
- Bandwidth (upload/download)
- RTT and jitter
- Loss/retransmit rates
- Reachability status

**Flow statistics**:
- Active TCP connections
- Active UDP mappings
- Total flows created

**Score history**:
- Time-series chart of upstream scores
- Switching events marked on chart

**Measurement status**:
- Last measurement time per upstream
- Next scheduled measurement
- Measurement errors

**Update mechanisms**:
- Upstream metrics (bandwidth, RTT, scores) are polled from `/metrics` endpoint at a user-selectable interval (1s, 3s, 5s, or 10s via UI buttons)
- Connection/flow events and measurement completions are pushed via WebSocket for real-time updates to the active connections list and test history

### Monitoring via Prometheus metrics

Prometheus metrics are exposed at `/metrics` endpoint:

```bash
curl -H "Authorization: Bearer <token>" http://127.0.0.1:8080/metrics
```

**Most commonly used metrics:**

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `fbforward_upstream_score` | Gauge | `upstream` | Final upstream quality score |
| `fbforward_upstream_reachable` | Gauge | `upstream` | Reachability (1=reachable, 0=unreachable) |
| `fbforward_active_upstream` | Gauge | `upstream` | Active upstream (1=active, 0=inactive) |
| `fbforward_tcp_active` | Gauge | - | Active TCP connections |
| `fbforward_udp_mappings_active` | Gauge | - | Active UDP mappings |
| `fbforward_bytes_up_total` | Counter | `upstream` | Total uploaded bytes |
| `fbforward_bytes_down_total` | Counter | `upstream` | Total downloaded bytes |
| `fbforward_upstream_bandwidth_up_bps` | Gauge | `upstream` | Upload bandwidth (bits/sec) |
| `fbforward_upstream_bandwidth_down_bps` | Gauge | `upstream` | Download bandwidth (bits/sec) |
| `fbforward_upstream_rtt_ms` | Gauge | `upstream` | Mean RTT (milliseconds) |
| `fbforward_upstream_jitter_ms` | Gauge | `upstream` | RTT jitter (milliseconds) |
| `fbforward_upstream_loss_rate` | Gauge | `upstream` | UDP loss rate [0, 1] |
| `fbforward_upstream_retrans_rate` | Gauge | `upstream` | TCP retransmit rate [0, 1] |
| `fbforward_measurement_queue_size` | Gauge | - | Pending measurements in queue |

For the complete metrics catalog including per-protocol bandwidth, utilization, and rate metrics, see [Section 5.2.4 Prometheus metrics](api-reference.md#524-prometheus-metrics).

Configure Prometheus scrape target:

```yaml
scrape_configs:
  - job_name: 'fbforward'
    static_configs:
      - targets: ['127.0.0.1:8080']
    bearer_token: '<token>'
```

### Log interpretation

fbforward logs to stderr using structured logging. Each log line includes:

- **Timestamp**: ISO 8601 format
- **Level**: INFO, WARN, ERROR
- **Message**: Human-readable description
- **Fields**: Key-value pairs for context

Common log patterns:

**Primary selection**:
```
INFO primary selected tag=backup score=0.92 reason="score delta" old_primary=primary
```
Indicates upstream switch. Check `tag` for new primary, `reason` for trigger.

**Measurement errors**:
```
WARN measurement failed tag=primary protocol=tcp error="dial timeout"
```
Indicates connectivity issue to measurement endpoint. Check fbmeasure status on upstream.

**Fast failover**:
```
INFO fast failover triggered tag=primary reason="high retransmit rate" rate=0.25
```
Indicates immediate switch due to quality degradation. Check network conditions.

**Unusable upstream**:
```
WARN upstream marked unusable tag=backup reason="consecutive dial failures" count=3
```
Indicates upstream excluded from selection. Check upstream connectivity.

**Usable upstream recovered**:
```
INFO upstream recovered tag=backup
```
Indicates previously unusable upstream is eligible again.

**Flow limits reached**:
```
WARN max TCP connections reached limit=50 rejected=1
```
Indicates connection limit hit. Consider increasing `forwarding.limits.max_tcp_connections`.

**Configuration reload**:
```
INFO restart requested
INFO config loaded path=config.yaml upstreams=2 listeners=2
INFO runtime stopped
INFO runtime started
```
Indicates successful configuration reload via RPC.

---

## 3.1.4 Troubleshooting

### Common error messages

**"config invalid: at least one upstream required"**

Cause: `upstreams` section is empty or missing.

Resolution: Add at least one upstream definition to configuration.

**"config invalid: at least one listener required"**

Cause: `forwarding.listeners` section is empty or missing.

Resolution: Add at least one listener definition to configuration.

**"startup failed: listen tcp 0.0.0.0:9000: bind: address already in use"**

Cause: Another process is listening on the configured port.

Resolution: Stop the conflicting process or change `bind_port` in configuration.

**"startup failed: operation not permitted"**

Cause: fbforward lacks required capabilities (CAP_NET_RAW for ICMP or CAP_NET_ADMIN for shaping).

Resolution: Assign capabilities with `setcap` or run via systemd with `AmbientCapabilities`.

```bash
sudo setcap cap_net_raw+ep ./fbforward
```

**"measurement failed: connection refused"**

Cause: fbmeasure is not running on upstream host or firewall blocks connection.

Resolution: Start fbmeasure on upstream and verify connectivity:

```bash
# On upstream
./fbmeasure --port 9876

# From fbforward host
./bwprobe -server <upstream>:9876 -bandwidth 10m
```

**"dial failed: no such host"**

Cause: Upstream hostname does not resolve via DNS.

Resolution: Verify DNS configuration or use IP address in `upstreams[].destination.host`.

**"measurement stale: falling back to ICMP"**

Cause: bwprobe measurements have not completed within `measurement.stale_threshold`.

Resolution: Check fbmeasure connectivity and network conditions. Review measurement logs for errors.

### Diagnostic checklist

When fbforward is not operating correctly, verify:

**1. Capabilities**:
```bash
getcap ./fbforward
# Expected: cap_net_raw=ep (at minimum)
```

**2. fbmeasure connectivity**:
```bash
# Test TCP connection to measurement endpoint
nc -zv <upstream-host> 9876

# Test bwprobe measurement
./bwprobe -server <upstream-host>:9876 -bandwidth 10m -samples 1
```

**3. DNS resolution**:
```bash
# Verify upstream hostnames resolve
dig <upstream-host>
```

**4. Listener ports**:
```bash
# Verify fbforward is listening
ss -tlnp | grep fbforward
ss -ulnp | grep fbforward
```

**5. Control plane access**:
```bash
# Test control plane connectivity
curl -H "Authorization: Bearer <token>" http://127.0.0.1:8080/metrics
```

**6. ICMP reachability**:
```bash
# Verify ICMP echo from fbforward host to upstreams
ping -c 3 <upstream-host>
```

**7. Firewall rules**:
```bash
# Verify no firewall blocks
iptables -L -n -v | grep <port>
```

**8. Log output**:
```bash
# Check for errors in logs
./fbforward --config config.yaml 2>&1 | grep ERROR
```

### Performance troubleshooting

**High latency**:

Check upstream RTT metrics in web UI or Prometheus. High RTT indicates network path issues.

Verify:
- Physical link quality (cable, WiFi signal)
- Upstream server load
- Network congestion between fbforward and upstream

**Low throughput**:

Check bandwidth metrics in web UI. Low bandwidth indicates link saturation or throttling.

Verify:
- Measurement target bandwidth matches link capacity
- No QoS policies throttling traffic
- Upstream has sufficient capacity

If traffic shaping is enabled, verify `shaping.aggregate_limit` and per-upstream limits are appropriate.

**Frequent switching**:

Check score history in web UI. Frequent switches indicate unstable network conditions or misconfigured switching policy.

Adjust:
- Increase `switching.auto.confirm_duration` (default 15s)
- Increase `switching.auto.min_hold_time` (default 30s)
- Increase `switching.auto.score_delta_threshold` (default 5.0)

**Upstream marked unusable**:

Check reachability and measurement logs. Unusable status indicates severe quality issues.

Verify:
- Upstream host is online
- fbmeasure is running on upstream
- No firewall blocks ICMP or measurement port
- No extreme packet loss or latency on link

**Flows rejected due to limits**:

Check logs for "max TCP connections reached" or "max UDP mappings reached".

Increase limits in configuration:
```yaml
forwarding:
  limits:
    max_tcp_connections: 100  # Default: 50
    max_udp_mappings: 1000    # Default: 500
```

### Log analysis patterns

**Identify switching events**:
```bash
grep "primary selected" fbforward.log
```

**Count measurement failures per upstream**:
```bash
grep "measurement failed" fbforward.log | awk '{print $NF}' | sort | uniq -c
```

**Check fast failover triggers**:
```bash
grep "fast failover" fbforward.log
```

**Monitor flow creation rate**:
```bash
grep "flow created" fbforward.log | awk '{print $1}' | uniq -c
```

**Find configuration reload events**:
```bash
grep "restart requested" fbforward.log
```

For structured log analysis, pipe stderr to a JSON log processor or use systemd journal queries:

```bash
journalctl -u fbforward -o json | jq 'select(.MESSAGE | contains("measurement failed"))'
```
