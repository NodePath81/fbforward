# fbmeasure user guide

This guide covers fbmeasure deployment, configuration, and operation.

---

## 3.3.1 Overview

### Purpose

fbmeasure is the measurement server binary that runs on upstream hosts. The server accepts bwprobe bandwidth test connections and reports receive-side statistics back to clients. fbforward requires fbmeasure running on each upstream to perform TCP and UDP link quality measurements.

Without fbmeasure, fbforward operates in degraded mode using ICMP reachability probes only. Scoring and upstream selection quality is significantly reduced without bandwidth measurements.

### Architecture

fbmeasure binds both TCP and UDP on a single port (default 9876) and handles:

**Control connections (TCP)**: Accepts bwprobe JSON-RPC commands for session management (`session.hello`, `session.goodbye`) and sample coordination (`sample.start`, `sample.stop`).

**Data connections (TCP and UDP)**: Accepts TCP or UDP data streams for bandwidth measurements. Tracks received bytes, timestamps, and loss/retransmit statistics. Aggregates data into 100ms intervals and reports per-interval metrics.

Each client session runs independently. The server supports concurrent sessions from multiple clients.

### Deployment requirements

fbmeasure must run on each upstream host that fbforward will select as a forwarding destination. The measurement endpoint is configured in fbforward's `upstreams[].measurement` section:

```yaml
upstreams:
  - tag: primary
    destination:
      host: upstream1.example.com
    measurement:
      host: upstream1.example.com
      port: 9876  # fbmeasure port (measurement traffic)
```

**Key points**:
- `destination.host` specifies the upstream hostname/IP for forwarded traffic
- `measurement.host` specifies the hostname/IP where fbmeasure runs (typically same as `destination.host`)
- `measurement.port` is the fbmeasure listen port (default 9876) where bwprobe tests connect
- Forwarding port is determined by the listener configuration (`forwarding.listeners[].bind_port`), not per-upstream

### Relationship to fbforward

fbforward connects to fbmeasure periodically to run bandwidth tests:

1. fbforward schedules measurements based on `measurement.schedule.interval` (default 15-45 minutes)
2. fbforward establishes control connection to `upstreams[].measurement.host:port`
3. fbforward runs TCP and UDP tests (upload and download)
4. fbmeasure reports metrics (bandwidth, RTT, loss/retransmit rates)
5. fbforward updates upstream scores using reported metrics
6. Measurement connection closes until next scheduled test

fbmeasure does not initiate connections or have awareness of fbforward's existence. It simply responds to bwprobe test requests from any client.

### Platform requirements

fbmeasure requires Linux. The server uses Linux-specific socket options (`TCP_INFO`, `SO_MAX_PACING_RATE`) for accurate measurement. Other platforms are not supported.

No special capabilities required. fbmeasure can run as an unprivileged user.

---

## 3.3.2 Configuration

fbmeasure configuration uses CLI flags. No configuration file is supported.

### CLI flag reference

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-port` | int | `9876` | Listen port for control and data connections |
| `-recv-wait` | duration | `100ms` | Receive wait timeout after sample stop |

**Listen port**: Specifies the TCP port fbmeasure binds to. Must match `measurement.port` in fbforward configuration.

**Receive wait**: Duration the server continues receiving data after a `sample.stop` command. This accommodates in-flight packets and ensures complete sample data collection. Default is 100ms, which is sufficient for most networks. Increase for high-latency or high-bandwidth×delay product links.

### Firewall requirements

fbmeasure requires inbound connections on the configured port. Configure firewall rules to allow TCP connections from fbforward host(s):

**iptables example**:
```bash
# Allow TCP connections to fbmeasure port from fbforward host
sudo iptables -A INPUT -p tcp --dport 9876 -s <fbforward-host-ip> -j ACCEPT
```

**ufw example**:
```bash
# Allow TCP port 9876 from specific IP
sudo ufw allow from <fbforward-host-ip> to any port 9876 proto tcp
```

**firewalld example**:
```bash
# Add rich rule for fbmeasure port
sudo firewall-cmd --permanent --add-rich-rule='rule family="ipv4" source address="<fbforward-host-ip>" port protocol="tcp" port="9876" accept'
sudo firewall-cmd --reload
```

Verify firewall allows connections:
```bash
# From fbforward host
nc -zv <upstream-host> 9876
```

### Port selection

Default port 9876 is chosen to avoid conflicts with common services. If port 9876 is unavailable, select an alternative:

```bash
# Check if port is in use
ss -tln | grep 9876

# Run fbmeasure on alternative port
./fbmeasure --port 9877
```

Update fbforward configuration to match:
```yaml
upstreams:
  - tag: primary
    measurement:
      host: upstream1.example.com
      port: 9877  # Must match fbmeasure port
```

---

## 3.3.3 Operation

### Starting as a service

For production deployment, run fbmeasure as a systemd service:

**Create service file** `/etc/systemd/system/fbmeasure.service`:

```ini
[Unit]
Description=fbmeasure bandwidth measurement server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=fbmeasure
Group=fbmeasure
ExecStart=/usr/local/bin/fbmeasure --port 9876
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

**Create unprivileged user**:
```bash
sudo useradd -r -s /bin/false fbmeasure
```

**Install binary**:
```bash
sudo cp fbmeasure /usr/local/bin/
sudo chown root:root /usr/local/bin/fbmeasure
sudo chmod 755 /usr/local/bin/fbmeasure
```

**Enable and start service**:
```bash
sudo systemctl daemon-reload
sudo systemctl enable fbmeasure
sudo systemctl start fbmeasure
```

**Check service status**:
```bash
sudo systemctl status fbmeasure
```

Expected output:
```
● fbmeasure.service - fbmeasure bandwidth measurement server
     Loaded: loaded (/etc/systemd/system/fbmeasure.service; enabled; preset: enabled)
     Active: active (running) since Sun 2025-01-26 12:00:00 UTC; 5min ago
   Main PID: 1234 (fbmeasure)
      Tasks: 3 (limit: 4694)
     Memory: 5.2M
        CPU: 120ms
     CGroup: /system.slice/fbmeasure.service
             └─1234 /usr/local/bin/fbmeasure --port 9876

Jan 26 12:00:00 upstream1 systemd[1]: Started fbmeasure bandwidth measurement server.
Jan 26 12:00:00 upstream1 fbmeasure[1234]: listening on port 9876
```

**View logs**:
```bash
sudo journalctl -u fbmeasure -f
```

### Running in foreground

For testing or debugging, run fbmeasure in foreground:

```bash
./fbmeasure --port 9876
```

Expected output:
```
listening on port 9876
```

fbmeasure logs to stdout/stderr. Normal operation produces minimal log output. Logs appear when clients connect:

```
client connected from 203.0.113.5:54321
session started id=abc123-def456
sample started protocol=tcp reverse=false
sample stopped duration=2.5s bytes=5242880
session ended id=abc123-def456
```

Stop with Ctrl+C or SIGTERM.

### Verifying connectivity

Test fbmeasure connectivity from fbforward host:

**Test TCP connection**:
```bash
nc -zv <upstream-host> 9876
```

Expected output:
```
Connection to <upstream-host> 9876 port [tcp/*] succeeded!
```

**Test bwprobe measurement**:
```bash
./bwprobe -target <upstream-host>:9876 -bandwidth 10m -sample-bytes 1mb -samples 1
```

If test completes successfully, fbmeasure is operational.

**Test from fbforward**:

Check fbforward logs for measurement results:
```bash
grep "measurement completed" fbforward.log
```

Expected log entry:
```
INFO measurement completed tag=primary protocol=tcp direction=upload bandwidth=48.5Mbps rtt=25ms
```

If logs show "measurement failed", check fbmeasure status and connectivity.

### Resource usage

fbmeasure resource consumption is minimal under normal load:

**Memory**: ~5-10 MB resident set size with no active sessions. Each active session adds ~1-2 MB for buffers.

**CPU**: Near zero when idle. During active measurements, CPU usage depends on bandwidth:
- 10 Mbps: < 1% CPU
- 100 Mbps: 1-2% CPU
- 1 Gbps: 5-10% CPU

CPU usage primarily from kernel packet processing, not fbmeasure process.

**Network**: Only bandwidth test traffic. No background traffic or keep-alives.

**Disk**: No disk I/O. All operations in memory.

### Monitoring

Monitor fbmeasure status using standard Linux tools:

**Check process**:
```bash
ps aux | grep fbmeasure
```

**Check listening port**:
```bash
ss -tln | grep 9876
```

**Monitor connections**:
```bash
ss -tn | grep 9876
```

Shows active client connections. Typically 2-3 connections per test (control + data channels).

**Monitor resource usage**:
```bash
top -p $(pgrep fbmeasure)
```

**Monitor network interface**:
```bash
ip -s link show <interface>
```

Watch RX/TX packets and bytes during measurements. Dropped packets indicate interface overload.

### Troubleshooting

**fbmeasure fails to start: "bind: address already in use"**

Cause: Another process is using port 9876.

Resolution:
```bash
# Find process using port
sudo ss -tlnp | grep 9876

# Kill conflicting process or use different port
./fbmeasure --port 9877
```

**Clients cannot connect: "connection refused"**

Cause: fbmeasure not running or firewall blocks connections.

Resolution:
1. Verify fbmeasure is running:
   ```bash
   ps aux | grep fbmeasure
   ss -tln | grep 9876
   ```
2. Check firewall rules
3. Test from fbforward host:
   ```bash
   nc -zv <upstream-host> 9876
   ```

**Measurements fail: "timeout"**

Cause: Network path has high latency or packet loss.

Resolution:
1. Test basic connectivity:
   ```bash
   ping <upstream-host>
   ```
2. Run manual bwprobe test from fbforward host:
   ```bash
   ./bwprobe -target <upstream-host> -bandwidth 10m -sample-bytes 1mb
   ```
3. Check fbmeasure logs for errors
4. Increase `-recv-wait` timeout if necessary:
   ```bash
   ./fbmeasure --port 9876 --recv-wait 500ms
   ```

**High CPU usage**

Cause: Frequent measurements or very high bandwidth tests.

Resolution:
1. Reduce measurement frequency in fbforward config:
   ```yaml
   measurement:
     schedule:
       interval:
         min: 30m  # Increase from default 15m
         max: 60m  # Increase from default 45m
   ```
2. Reduce target bandwidth if link capacity is lower
3. Verify no other processes competing for CPU

**Inconsistent results**

Cause: Competing traffic or server resource constraints.

Resolution:
1. Check for other network-intensive processes:
   ```bash
   iotop
   iftop
   ```
2. Verify adequate CPU and memory available
3. Run isolated tests using bwprobe CLI to establish baseline
4. Compare fbforward measurement results to manual bwprobe tests

### Upgrading

To upgrade fbmeasure:

1. Stop service:
   ```bash
   sudo systemctl stop fbmeasure
   ```
2. Replace binary:
   ```bash
   sudo cp fbmeasure /usr/local/bin/
   ```
3. Start service:
   ```bash
   sudo systemctl start fbmeasure
   ```

No configuration migration required (fbmeasure has no config file). Service restart terminates active measurement sessions; clients will reconnect.

### Security considerations

**Unprivileged operation**: fbmeasure does not require root or special capabilities. Run as dedicated unprivileged user.

**No authentication**: fbmeasure does not authenticate clients. Any host that can connect to the port can run measurements. Restrict access using firewall rules.

**Resource exhaustion**: Malicious clients could potentially exhaust memory or bandwidth by initiating many concurrent sessions. Deploy rate limiting at firewall level if exposure to untrusted networks is a concern:

```bash
# Example: Limit connection rate with iptables
sudo iptables -A INPUT -p tcp --dport 9876 --syn -m recent --set
sudo iptables -A INPUT -p tcp --dport 9876 --syn -m recent --update --seconds 60 --hitcount 10 -j DROP
```

**No encryption**: Control and data channels are unencrypted. Measurements reveal bandwidth capacity and timing information. Deploy within trusted networks or use VPN/WireGuard tunnel if confidentiality is required.
