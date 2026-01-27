# bwprobe user guide

This guide covers bwprobe operation, configuration, and troubleshooting.

---

## 3.2.1 Overview

### What bwprobe measures

bwprobe is a network quality measurement tool that tests bandwidth at a specified rate cap. The tool measures:

**Throughput**: Achieved bandwidth during sample transfers. Reported as:
- [Trimmed mean](glossary.md#trimmed-mean): Average throughput after dropping top/bottom 10% of interval rates
- [Sustained peak](glossary.md#sustained-peak): Maximum average throughput over rolling 1-second window
- Percentiles (P90, P80): 90th and 80th percentile of interval rates

**[RTT](glossary.md#rtt) (round-trip time)**: Latency between client and server. Sampled continuously during tests at configurable rate (default 10 samples/sec). Reported as mean, min, max, and [jitter](glossary.md#jitter) (standard deviation).

**[Loss rate](glossary.md#loss-rate) (UDP)**: Fraction of packets not received. Computed as (sent - received) / sent.

**[Retransmit rate](glossary.md#retransmit-rate) (TCP)**: Fraction of TCP segments retransmitted. Derived from `TCP_INFO` socket statistics.

### Two-channel design

bwprobe uses separate [control channel](glossary.md#control-channel) and [data channel](glossary.md#data-channel) connections:

**Control channel**: TCP connection carrying JSON-RPC 2.0 messages for session management and sample coordination. The client sends commands (`session.hello`, `sample.start`, `sample.stop`, `session.goodbye`) and receives reports with per-sample metrics.

**Data channel**: TCP or UDP stream for actual bandwidth measurement data. The channel transfers fixed-size payload bytes per sample at the target rate using kernel pacing (`SO_MAX_PACING_RATE`). Data frames include headers with sequence numbers for loss detection.

This design isolates measurement traffic from control messages, preventing control overhead from biasing throughput results.

### Sample-based testing model

Each test run executes a fixed number of [samples](glossary.md#sample):

1. Client sends `SAMPLE_START` (or `SAMPLE_START_REVERSE` for download) on control channel
2. Data transfer runs at target rate until [sample size](glossary.md#sample-size) payload bytes are sent
3. Client sends `SAMPLE_STOP` on control channel
4. Server aggregates data into 100ms [intervals](glossary.md#interval) and computes metrics
5. Server returns sample report with interval stats, throughput estimates, RTT statistics, and loss/retransmit counts

Multiple samples per test provide statistical confidence. The default configuration runs 10 samples.

### Upload vs download testing

**Upload test** (default): Client sends data to server. Measures upstream link quality from client perspective.

**Download test** (`-reverse` flag): Server sends data to client. Measures downstream link quality from client perspective.

In download mode, the client still drives control (initiates samples and requests reports). The server becomes the data sender and reports its TCP retransmit statistics (since it is the sender).

### Reverse mode differences

When `-reverse` is specified:

- Server establishes data channel back to client
- Client receives data at target rate
- Server reports send-side TCP statistics (retransmits)
- Client reports receive-side UDP statistics (loss)
- RTT measurement direction unchanged (client always measures)

---

## 3.2.2 Configuration

bwprobe configuration uses CLI flags. No configuration file is supported.

### CLI flag reference

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-mode` | string | `client` | Mode: `server` or `client` |
| `-port` | int | `9876` | Port for control and data |
| `-target` | string | `localhost` | Target host (client mode) |
| `-network` | string | `tcp` | Protocol: `tcp` or `udp` |
| `-bandwidth` | string | *(required)* | Target bandwidth (e.g., `10m`, `100m`, `1g`) |
| `-sample-bytes` | string | *(required)* | Payload bytes per sample (e.g., `500kb`, `5mb`) |
| `-samples` | int | `10` | Number of samples to run |
| `-wait` | duration | `0` | Pause between samples (e.g., `1s`, `500ms`) |
| `-max-duration` | duration | `0` | Max test duration (0 = unlimited) |
| `-rtt-rate` | int | `10` | RTT samples per second |
| `-chunk-size` | string | `1200B` | Chunk size including headers (e.g., `1.2kb`, `64kb`) |
| `-reverse` | bool | `false` | Download test (server → client) |
| `-no-progress` | bool | `false` | Disable progress bar |
| `-recv-wait` | duration | `500ms` | Server receive window after sample stop (server mode) |

**Bandwidth format**: Number followed by unit suffix. Valid suffixes: `k` (Kbps), `m` (Mbps), `g` (Gbps). Examples: `10m` = 10 Mbps, `1g` = 1 Gbps.

**Bytes format**: Number followed by unit suffix. Valid suffixes: `b` (bytes), `kb` (kilobytes), `mb` (megabytes), `gb` (gigabytes). Examples: `500kb` = 500 KB, `5mb` = 5 MB.

**Duration format**: Number followed by unit suffix. Valid suffixes: `ms` (milliseconds), `s` (seconds), `m` (minutes), `h` (hours). Examples: `500ms`, `30s`, `1m`.

### Target bandwidth

The `-bandwidth` flag sets the target sending rate. Linux kernel pacing (`SO_MAX_PACING_RATE`) enforces the rate at the socket level. The measurement reports achieved bandwidth relative to this target.

Set `-bandwidth` to the expected link capacity or slightly below. If the target exceeds actual link capacity, congestion and loss will occur, affecting results.

### Sample configuration

The `-sample-bytes` flag controls how much data is transferred per sample. Larger samples provide more stable measurements but take longer to complete.

Recommended values:
- Fast links (>100 Mbps): 5 MB per sample
- Medium links (10-100 Mbps): 1-5 MB per sample
- Slow links (<10 Mbps): 500 KB per sample

The `-samples` flag sets the number of samples. More samples improve confidence but extend test duration. Default is 10 samples.

The `-wait` flag inserts a pause between samples. Use this to avoid sustained bursts or to space measurements over time.

### Chunk size

The `-chunk-size` flag sets the size of individual data frames. Smaller chunks provide finer pacing granularity but increase syscall overhead. Larger chunks reduce overhead but coarsen pacing.

Default is 1200 bytes (suitable for avoiding fragmentation on typical MTUs). For high-bandwidth tests (>500 Mbps), consider larger chunks (e.g., `64kb`).

TCP payload size is chunk size minus frame header (16 bytes). UDP payload size is constrained by socket buffer limits (64 KB max).

### Timeout settings

The `-max-duration` flag caps total test duration. If samples do not complete within this window, the test terminates early and reports partial results.

The `-recv-wait` flag (server mode only) sets how long the server continues receiving after a `sample.stop` command. This accommodates in-flight packets. Default is 500ms.

### Output format

bwprobe writes results to stdout in text format. Each test produces:

- Test configuration summary
- Per-sample progress (unless `-no-progress`)
- Aggregate results: duration, bytes, bandwidth estimates
- RTT statistics: mean, min, max, jitter, sample count
- Loss/retransmit statistics

For programmatic parsing, use the `bwprobe/pkg` Go library and access structured result types. See [Section 5.1](api-reference.md#51-bwprobe-public-api).

---

## 3.2.3 Operation

### Running TCP upload test

Measure upload bandwidth to a server running fbmeasure or bwprobe in server mode:

```bash
./bwprobe -target example.com -bandwidth 50m -sample-bytes 5mb -samples 5
```

Expected output:

```
=== Network Quality Test (Client) ===
Target: example.com:9876
Protocol: TCP

Test Configuration:
  Target bandwidth:  50.00 Mbps
  Role:              Client (requester)
  Traffic direction: Upload (client -> server)
  Samples:           5
  Sample bytes:      5.00 MB
  Wait:              0s
  Max duration:      Unlimited
  RTT sample rate:   10 samples/sec
  TCP send buffer (est.): 312.50 KB
  Chunk size:        1.17 KB

[Test] ████████████████████ 100% | Sample 5/5 complete

Test Results:
  Duration:           15.2s
  Bytes sent:         25.00 MB
  Achieved bandwidth (trimmed mean): 48.5 Mbps (97.0% of target)
  Samples:            5/5
  TCP send buffer:    512.00 KB

Bandwidth Estimates (server intervals):
  Sustained peak (1s): 49.2 Mbps
  Trimmed mean:        48.5 Mbps
  P90:                 49.0 Mbps
  P80:                 48.8 Mbps

RTT Statistics:
  Mean:    25ms
  Min:     23ms
  Max:     28ms
  Jitter:  1.2ms (stdev)
  Samples: 152

TCP Retransmits:
  Retransmits:  3
  Segments sent: 18432
  Loss rate:    0.0163% (3/18432)
```

### Running TCP download test

Measure download bandwidth using `-reverse`:

```bash
./bwprobe -target example.com -bandwidth 200m -sample-bytes 10mb -samples 3 -reverse
```

In reverse mode, the server sends data to the client. The server reports retransmit statistics.

### Running UDP test

Measure UDP bandwidth and loss rate:

```bash
./bwprobe -target example.com -network udp -bandwidth 50m -sample-bytes 5mb -samples 5
```

Expected output (loss section):

```
UDP Packet Loss:
  Packets sent:     4267
  Packets received: 4265
  Packets lost:     2
  Loss rate:        0.0469%
```

UDP tests report packet loss computed from sequence numbers in frame headers. Server tracks received sequence numbers and reports gaps.

### Interpreting throughput results

**Trimmed mean**: The primary bandwidth metric. Represents achieved bandwidth with outliers removed. Compare this to the target bandwidth to assess link utilization.

- Utilization near 100%: Link is saturated or pacing is accurate
- Utilization below 90%: Link may have more capacity, or congestion is limiting throughput
- Utilization above 100%: Should not occur (indicates measurement error)

**Sustained peak**: Highest average throughput over any 1-second window. Indicates burst capacity or transient high throughput.

**Percentiles (P90/P80)**: Throughput values at 90th and 80th percentiles of interval rates. Useful for understanding variance.

### Interpreting RTT results

**Mean RTT**: Average round-trip time. Typical values:
- < 10ms: Local network or nearby server
- 10-50ms: Regional connection
- 50-150ms: Intercontinental connection
- > 150ms: High-latency link or congestion

**Jitter**: Standard deviation of RTT samples. Indicates latency stability.
- < 2ms: Very stable link
- 2-10ms: Moderate variance
- > 10ms: High variance, possible congestion

**Min/Max**: Lowest and highest RTT observed. Large difference (> 50ms) suggests intermittent congestion or bufferbloat.

### Interpreting loss and retransmit rates

**TCP retransmit rate**: Fraction of segments retransmitted.
- < 0.01% (0.0001): Excellent
- 0.01-0.1%: Good
- 0.1-1%: Fair, indicates minor loss
- > 1%: Poor, significant loss or congestion

**UDP loss rate**: Fraction of packets not received.
- 0%: Perfect delivery
- < 0.1%: Excellent
- 0.1-1%: Acceptable for real-time applications
- > 1%: Poor for real-time, may affect voice/video quality

### Comparing upload vs download

Run both upload and download tests to understand asymmetric link characteristics:

```bash
# Upload
./bwprobe -target example.com -bandwidth 50m -sample-bytes 5mb

# Download
./bwprobe -target example.com -bandwidth 200m -sample-bytes 10mb -reverse
```

Asymmetric links (e.g., cable/DSL with lower upload capacity) show different throughput and loss rates in each direction.

---

## 3.2.4 Troubleshooting

### Connection failures

**"connection refused"**

Cause: Server is not listening on the specified port or firewall blocks connection.

Resolution:
1. Verify server is running:
   ```bash
   # On server host
   ss -tln | grep 9876
   ```
2. Test TCP connectivity:
   ```bash
   nc -zv <server-host> 9876
   ```
3. Check firewall rules on server

**"dial timeout"**

Cause: Network path is unreachable or severely congested.

Resolution:
1. Verify server host is reachable:
   ```bash
   ping <server-host>
   ```
2. Check for packet loss or high latency:
   ```bash
   mtr <server-host>
   ```
3. Increase timeout (not configurable in CLI, use `bwprobe/pkg` API)

**"no such host"**

Cause: Hostname does not resolve.

Resolution:
1. Verify DNS resolution:
   ```bash
   dig <hostname>
   ```
2. Use IP address instead of hostname

### Timeout issues

**Test takes longer than expected**

Cause: Actual link bandwidth is lower than target, or packet loss requires retransmissions.

Calculation: Expected duration = (sample_bytes × samples × 8) / bandwidth_bps

Example:
- Sample bytes: 5 MB
- Samples: 10
- Bandwidth: 50 Mbps
- Expected: (5 MB × 10 × 8 bits) / 50 Mbps = 8 seconds

If test takes significantly longer, link may be slower than specified target.

Resolution: Reduce `-bandwidth` target or use `-max-duration` to cap test time.

**Test terminates early**

Cause: `-max-duration` cap exceeded or server forcibly closes connection.

Resolution:
1. Increase `-max-duration` value
2. Reduce number of samples
3. Check server logs for errors

### Measurement anomalies

**Achieved bandwidth significantly below target**

Possible causes:
- Link capacity is lower than target
- Congestion on network path
- TCP send buffer too small (check output for "TCP send buffer" line)
- Server receive buffer insufficient

Resolution:
1. Reduce target bandwidth to match actual capacity
2. Check for congestion with `mtr` or `traceroute`
3. Increase TCP buffer sizes at OS level:
   ```bash
   # Increase TCP buffers (Linux)
   sysctl -w net.ipv4.tcp_wmem="4096 65536 16777216"
   sysctl -w net.ipv4.tcp_rmem="4096 87380 16777216"
   ```

**High jitter or RTT variance**

Possible causes:
- Network congestion
- Bufferbloat (excessive buffering in routers)
- Wireless link with variable latency

Resolution:
1. Test at different times of day to identify congestion patterns
2. Reduce bandwidth target to avoid filling buffers
3. Check wireless signal quality if applicable

**High retransmit/loss rate**

Possible causes:
- Network congestion
- Link errors (physical layer issues)
- MTU mismatches causing fragmentation

Resolution:
1. Reduce bandwidth target
2. Check physical link quality (cable, WiFi signal)
3. Verify MTU configuration:
   ```bash
   ip link show <interface> | grep mtu
   ```
4. Try smaller chunk sizes:
   ```bash
   ./bwprobe -target example.com -bandwidth 50m -sample-bytes 5mb -chunk-size 512B
   ```

**Inconsistent results across samples**

Possible causes:
- Network conditions changing during test
- Cross-traffic interfering with measurements
- Shared link with variable utilization

Resolution:
1. Increase number of samples to average out variance
2. Add wait time between samples:
   ```bash
   ./bwprobe -target example.com -bandwidth 50m -sample-bytes 5mb -samples 20 -wait 1s
   ```
3. Run tests at different times to identify patterns

### Server-side diagnostics

If measurements fail or show anomalies, check server status:

**Verify server is running**:
```bash
# On server host
ps aux | grep bwprobe
# or
ps aux | grep fbmeasure
```

**Check server logs**:
```bash
# If running via systemd
journalctl -u fbmeasure -f

# If running in foreground, check stderr output
```

**Monitor server resource usage**:
```bash
# CPU and memory
top

# Network interface stats
ip -s link show <interface>
```

High CPU usage or dropped packets at the interface indicate server-side bottleneck.

**Test server responsiveness**:
```bash
# Run minimal test
./bwprobe -target <server-host> -bandwidth 1m -sample-bytes 100kb -samples 1
```

If minimal test succeeds but larger tests fail, server may have resource constraints.

### Network debugging tools

Useful tools for diagnosing network issues:

**mtr**: Combined traceroute and ping
```bash
mtr -n -c 100 <server-host>
```
Shows per-hop packet loss and latency.

**iperf3**: Alternative bandwidth testing tool
```bash
# Server
iperf3 -s

# Client
iperf3 -c <server-host> -b 50M
```
Compare iperf3 results to bwprobe to isolate measurement issues.

**tcpdump**: Packet capture for deep inspection
```bash
# Capture bwprobe traffic
sudo tcpdump -i <interface> -w capture.pcap host <server-host> and port 9876
```
Analyze capture with Wireshark to inspect frame timing and retransmissions.

**ss**: Socket statistics
```bash
# Show TCP info for active connection
ss -ti dst <server-host>
```
Displays retransmit counts, RTT, and congestion window.
