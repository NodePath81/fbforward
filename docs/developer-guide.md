# Developer guide

This guide provides architecture details and contribution guidelines for developers working on fbforward and bwprobe.

---

## 7.1 Architecture deep dive

### Package dependency graph

The fbforward repository is organized as a monorepo containing two main projects with a shared measurement tool:

**fbforward packages:**

- `cmd/fbforward/` - Main entry point, CLI flag parsing, signal handling
- `internal/app/` - Application lifecycle (Supervisor, Runtime)
- `internal/config/` - Configuration loading, validation, custom unmarshaling
- `internal/upstream/` - Upstream state, scoring, switching logic
- `internal/forwarding/` - TCP/UDP listeners and proxy logic
- `internal/control/` - HTTP server, RPC handlers, WebSocket status stream
- `internal/metrics/` - Prometheus metrics aggregation
- `internal/probe/` - ICMP echo probing for reachability
- `internal/measure/` - bwprobe measurement orchestration, fast-start, scheduling
- `internal/resolver/` - DNS resolution with configurable strategy
- `internal/shaping/` - Linux tc traffic shaping (HTB qdisc, IFB device)
- `internal/util/` - Logger, helpers, type utilities
- `internal/version/` - Version string injection
- `web/` - Embedded web UI (TypeScript, Vite-built, served via `//go:embed`)

**bwprobe packages:**

- `bwprobe/cmd/` - CLI tool entry point
- `bwprobe/cmd/fbmeasure/` - Measurement server binary
- `bwprobe/pkg/` - Public API for embedding (`github.com/NodePath81/fbforward/bwprobe/pkg`)
- `bwprobe/internal/engine/` - Test orchestration, sample loop, metric computation
- `bwprobe/internal/rpc/` - JSON-RPC 2.0 client/server, session management
- `bwprobe/internal/server/` - Control listener, data stream routing
- `bwprobe/internal/transport/` - Reverse-mode connection handling
- `bwprobe/internal/network/` - TCP/UDP senders with kernel pacing
- `bwprobe/internal/metrics/` - RTT sampler, TCP_INFO reader, UDP loss tracking
- `bwprobe/internal/protocol/` - Data frame headers and constants

**Dependency flow:**

```
cmd/fbforward
  └─> internal/app (Supervisor, Runtime)
       ├─> internal/config (Config loading)
       ├─> internal/upstream (UpstreamManager)
       ├─> internal/forwarding (TCPListener, UDPListener)
       ├─> internal/control (ControlServer, StatusStore)
       ├─> internal/metrics (Metrics)
       ├─> internal/probe (ProbeLoop)
       ├─> internal/measure (Collector, Scheduler)
       │    └─> bwprobe/pkg (Run, MeasureRTT)
       │         └─> bwprobe/internal/engine
       │              └─> bwprobe/internal/rpc (Client)
       ├─> internal/resolver (Resolver)
       ├─> internal/shaping (TrafficShaper)
       └─> web (WebUIHandler)
```

**Key invariants:**

- `internal/` packages are private to fbforward; only `bwprobe/pkg/` is public API
- `bwprobe/internal/` packages must not import any `internal/` packages from fbforward
- `web/` embeds built TypeScript UI; Go code depends only on `embed.FS`
- Configuration flows from `internal/config` to all other packages via `Runtime`

### Concurrency model

fbforward runs a single process with multiple goroutines for parallel I/O and background tasks.

**Goroutine spawn points:**

| Location | Purpose | Count | Lifecycle |
|----------|---------|-------|-----------|
| `Runtime.startProbes()` | ICMP probe loop per upstream | 1 per upstream | Terminated by context cancel |
| `Runtime.startMeasurement()` | bwprobe measurement scheduler | 1 | Terminated by context cancel |
| `Runtime.startDNSRefresh()` | DNS re-resolution for non-IP upstreams | 1 per upstream | Terminated by context cancel |
| `Runtime.startListeners()` | Accept loop for TCP/UDP listeners | 1 per listener | Terminated by listener close |
| `TCPListener.handleConn()` | Per-connection proxy goroutine | 1 per TCP connection | Terminates on connection close or idle timeout |
| `UDPListener.Start()` | Per-mapping relay goroutine | 1 per UDP 5-tuple mapping | Terminates on idle timeout or context cancel |
| `ControlServer.Start()` | HTTP server handler goroutines | 1 per request (managed by `http.Server`) | Terminates on request completion |
| `StatusHub.Run()` | WebSocket broadcast loop | 1 | Terminated by context cancel |
| `Metrics.Start()` | Periodic metric updates | 1 | Terminated by context cancel |

**Context propagation:**

fbforward uses a single root context created in `Runtime.NewRuntime()`:

```go
ctx, cancel := context.WithCancel(context.Background())
```

All goroutines receive either:
- The root context directly (for background loops)
- A timeout-scoped child context (for time-limited operations like bwprobe tests)

Shutdown sequence:
1. `Runtime.Stop()` calls `cancel()` to signal all goroutines
2. `StatusStore.CloseAll()` forcibly closes all active TCP connections and UDP mappings
3. Listener `Close()` stops accept loops
4. `control.Server.Shutdown()` drains HTTP requests with 2-second timeout
5. `sync.WaitGroup.Wait()` blocks until all background goroutines exit

**Concurrency patterns:**

- **Fan-out probing**: Each upstream gets independent probe/measurement goroutine
- **Per-flow isolation**: TCP connections and UDP mappings run in separate goroutines with pinned upstream
- **Broadcast updates**: `StatusHub` broadcasts state changes to all WebSocket clients
- **Lock-free reads**: Metrics use atomic operations or mutex-protected snapshots

**Synchronization primitives:**

- `sync.Mutex` in `UpstreamManager` protects mode, activeTag, switching state
- `sync.RWMutex` in `StatusStore` protects TCP/UDP flow maps
- `sync.WaitGroup` in `Runtime` tracks all background goroutines
- `sync.Pool` in `forwarding` reuses buffers for TCP proxy
- Atomic operations in `Metrics` for counters

### State management patterns

**Upstream state:**

Each `Upstream` instance maintains:
- Resolved IPs (updated by DNS refresh goroutine)
- Active IP selection (round-robin on consecutive failures)
- EMA-smoothed metrics (bandwidth, RTT, jitter, loss/retrans rates)
- Reachability status (updated by probe loop)
- Measurement timestamps (last successful measurement)
- Dial failure tracking (consecutive failures, cooldown expiry)

State updates are synchronized through `UpstreamManager`:

```go
manager.MarkDialFailure(tag, cooldown)  // Increment failure count, set cooldown
manager.ClearDialFailure(tag)           // Reset on successful dial
manager.UpdateResolved(tag, ips)        // DNS refresh
```

**Flow table state:**

`StatusStore` maintains two flow tables:
- TCP: `map[string]*tcpFlow` keyed by `(proto,srcIP,srcPort,dstIP,dstPort)`
- UDP: `map[string]*udpMapping` keyed by `(proto,srcIP,srcPort,dstIP,dstPort)`

Each flow stores:
- Pinned upstream tag (never changes for flow lifetime)
- Connection/socket handles (for force-close on shutdown)
- Traffic counters (bytes up/down)
- Activity timestamps (for idle timeout detection)

Flow lifecycle:
1. Create on first packet/connection with current primary upstream
2. Pin to that upstream for lifetime
3. Remove on FIN/RST (TCP) or idle timeout (UDP)

**Primary upstream selection:**

`UpstreamManager` tracks:
- Current mode (`ModeAuto` or `ModeManual`)
- Active upstream tag (primary for new flows)
- Pending switch state (candidate tag, confirmation start time)
- Warmup mode (relaxed switching for first 15 seconds)

Switching decision flow (auto mode):
1. Periodic scoring computes final scores for all usable upstreams
2. If candidate score exceeds active + delta threshold, start confirmation timer
3. If candidate sustains advantage for `confirm_duration`, switch
4. If advantage lost, clear pending switch
5. Fast failover bypasses confirmation on high loss/retrans or consecutive dial failures

**Configuration reload:**

On `Restart` RPC call:
1. `Supervisor.Restart()` calls `Runtime.Stop()` on current runtime
2. Load new config from disk
3. Create fresh `Runtime` with new config
4. Start all goroutines with new parameters
5. Existing flows are terminated (no migration)

**Measurement queue and scheduler:**

The measurement scheduler (`internal/measure/scheduler.go`) manages when bwprobe tests run to avoid saturating the link.

Queue mechanics:
1. `Schedule(upstream, protocol, direction)` adds test to queue with scheduled timestamp
2. Queue processes entries in FIFO order respecting minimum inter-upstream gap
3. Tests are skipped if link utilization exceeds headroom threshold
4. Skip logic compares current traffic rate (from metrics) + required test bandwidth against configured limits

Headroom gating:
- Aggregate limit: Total link capacity (`shaping.aggregate_limit` or unlimited if shaping disabled)
- Per-upstream limits: Optional per-upstream bandwidth caps (`upstreams[].shaping`)
- Required headroom: `measurement.schedule.headroom.required_free_bandwidth` (bytes/sec)
- Max utilization: `measurement.schedule.headroom.max_link_utilization` (fraction, e.g., 0.7 = 70%)

Skip decision:
```
currentRate = metrics.GetRecentRate(rateWindow)
testBandwidth = max(tcpTargetUp, tcpTargetDown, udpTargetUp, udpTargetDown)
required = currentRate + testBandwidth + requiredHeadroom

if aggregateLimit > 0:
    utilizationFraction = required / aggregateLimit
    if utilizationFraction > maxUtilization:
        skip()
```

Observability:
- `GetScheduleStatus` RPC: Returns queue length, next scheduled time, last measurements per upstream, skipped count
- `GetQueueStatus` RPC: Returns detailed queue state (running tests, pending entries with timestamps)
- `fbforward_measurement_queue_size` gauge: Current queue depth
- `fbforward_measurement_skipped_total` counter: Cumulative skip count
- `fbforward_measurement_duration_seconds` histogram: Test durations

Data flow:
1. Scheduler goroutine calls `Collector.RunLoop()`
2. Loop dequeues next test, checks headroom
3. If sufficient headroom, calls `Collector.RunProtocol(ctx, upstream, protocol)`
4. `RunProtocol` runs TCP/UDP upload/download via bwprobe
5. Results passed to `UpstreamManager` for scoring update
6. Metrics updated, WebSocket `test_complete` event broadcast
7. UI polls `/metrics` for updated scores, WebSocket shows test history

### Error handling conventions

**Logging levels:**

- `logger.Error()` - Unrecoverable errors requiring user intervention (config load failure, listener bind failure)
- `logger.Warn()` - Recoverable errors or degraded state (measurement timeout, DNS resolution failure)
- `logger.Info()` - Normal operational events (upstream switch, listener start/stop, measurement completion)
- `logger.Debug()` - Verbose diagnostic information (connection accept, dial attempts)

**Error propagation:**

fbforward follows Go conventions:
- Functions return `(result, error)` pairs
- Errors are wrapped with context using `fmt.Errorf("context: %w", err)`
- Goroutines log errors directly rather than returning them (no error channel pattern)

**Graceful degradation:**

- **Missing measurement server**: Falls back to ICMP-only reachability if bwprobe measurements fail
- **DNS failure**: Continues using last-resolved IPs, logs warning
- **ICMP probe failure**: Marks upstream unreachable but retries indefinitely
- **Measurement timeout**: Skips sample, schedules next measurement normally
- **Dial failure**: Marks upstream unusable after consecutive failures, auto-recovers on success
- **Configuration validation**: Returns error on load, does not apply partial config

**Fatal errors:**

fbforward exits (via `log.Fatal` or returned error) on:
- Configuration file not found or unparseable
- Listener bind failure (port already in use, insufficient permissions)
- Shaping setup failure when shaping is enabled
- Platform check failure (non-Linux OS)

**Non-fatal errors:**

Logged but operation continues:
- Measurement test failure
- ICMP probe timeout
- DNS resolution timeout
- TCP dial failure
- Traffic shaping update failure (uses previous rules)

---

## 7.2 Extension points

### Adding new protocol forwarders

To add support for a new protocol (e.g., SCTP, QUIC), follow the TCP/UDP pattern in `internal/forwarding/`.

**Step 1: Define listener config**

Add protocol to `ListenerConfig` in [internal/config/config.go](../internal/config/config.go):

```go
type ListenerConfig struct {
    Protocol string `yaml:"protocol"` // "tcp", "udp", "sctp"
    BindAddr string `yaml:"bind_addr"`
    BindPort int    `yaml:"bind_port"`
}
```

**Step 2: Implement listener**

Create `internal/forwarding/forward_<protocol>.go`:

```go
type SCTPListener struct {
    cfg     config.ListenerConfig
    manager *upstream.UpstreamManager
    metrics *metrics.Metrics
    status  *control.StatusStore
    logger  util.Logger
}

func NewSCTPListener(cfg config.ListenerConfig, manager *upstream.UpstreamManager, metrics *metrics.Metrics, status *control.StatusStore, logger util.Logger) *SCTPListener {
    return &SCTPListener{cfg: cfg, manager: manager, metrics: metrics, status: status, logger: logger}
}

func (l *SCTPListener) Start(ctx context.Context, wg *sync.WaitGroup) error {
    // Bind listener
    // Launch accept loop
    // For each accepted flow:
    //   1. SelectUpstream() to get pinned upstream
    //   2. Dial upstream
    //   3. Spawn per-flow proxy goroutine
    //   4. Register flow in StatusStore
}

func (l *SCTPListener) Close() error {
    // Close listener socket
}
```

**Step 3: Register in Runtime**

Update `Runtime.startListeners()` in [internal/app/runtime.go](../internal/app/runtime.go):

```go
switch ln.Protocol {
case "tcp":
    // existing TCP code
case "udp":
    // existing UDP code
case "sctp":
    sctpListener := forwarding.NewSCTPListener(ln, r.manager, r.metrics, r.status, r.logger)
    if err := sctpListener.Start(r.ctx, &r.wg); err != nil {
        return err
    }
    r.listeners = append(r.listeners, sctpListener)
}
```

**Step 4: Add metrics**

Extend `Metrics` in [internal/metrics/metrics.go](../internal/metrics/metrics.go):

```go
sctpActiveGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
    Name: "fbforward_sctp_active",
    Help: "Number of active SCTP associations",
}, []string{"upstream"})

sctpBytesUpCounter := prometheus.NewCounterVec(prometheus.CounterOpts{
    Name: "fbforward_sctp_bytes_up_total",
    Help: "Total bytes sent upstream via SCTP",
}, []string{"upstream"})
```

**Step 5: Update StatusStore**

Add SCTP flow tracking to `StatusStore` in [internal/control/status.go](../internal/control/status.go):

```go
type StatusStore struct {
    tcpFlows  map[string]*tcpFlow
    udpFlows  map[string]*udpMapping
    sctpFlows map[string]*sctpAssoc  // Add this
    mu        sync.RWMutex
}
```

### Extending the scoring algorithm

Scoring logic lives in `UpstreamManager.computeScores()` in [internal/upstream/upstream.go](../internal/upstream/upstream.go).

**Adding a new sub-score:**

Example: Add jitter variance as sub-score.

**Step 1: Add metric to Upstream**

```go
type UpstreamStats struct {
    RTTMs         float64
    JitterMs      float64
    JitterVarMs   float64  // Add this
    // ... other fields
}
```

**Step 2: Collect metric in measurement**

Update `Collector.processResults()` in [internal/measure/collector.go](../internal/measure/collector.go):

```go
up.JitterVarMs = computeJitterVariance(result.RTT.Samples)
```

**Step 3: Add reference value and weight**

Update `ScoringConfig` in [internal/config/config.go](../internal/config/config.go):

```go
type ScoringReferenceConfig struct {
    JitterVar string `yaml:"jitter_var"` // e.g., "10ms"
}

type ScoringWeightsConfig struct {
    JitterVar float64 `yaml:"jitter_var"` // e.g., 0.1
}
```

**Step 4: Compute sub-score**

In `computeScores()`:

```go
jitterVarRef := parseReference(cfg.Reference.JitterVar, 10.0) // ms
sJitterVar := max(1 - exp(-up.JitterVarMs / jitterVarRef), epsilon)
```

**Step 5: Include in quality score**

```go
quality := pow(sBandwidthUp * sBandwidthDown * sRTT * sJitter * sJitterVar * ..., 1.0 / numSubScores)
```

**Modifying utilization penalty:**

The penalty curve is in `applyUtilizationPenalty()`:

```go
func applyUtilizationPenalty(baseScore float64, utilization float64, cfg config.UtilizationPenaltyConfig) float64 {
    if utilization < cfg.Threshold {
        return baseScore
    }
    overage := utilization - cfg.Threshold
    penalty := cfg.Multiplier * pow(overage, cfg.Exponent)
    return baseScore * (1 - penalty)
}
```

Modify curve shape by changing `Multiplier` or `Exponent` in config.

### Adding new RPC methods

Control plane RPC methods are in `ControlServer.handleRPC()` in [internal/control/control.go](../internal/control/control.go).

**Step 1: Define request/response types**

```go
type customMethodParams struct {
    Param1 string `json:"param1"`
    Param2 int    `json:"param2"`
}

type customMethodResponse struct {
    Result string `json:"result"`
}
```

**Step 2: Add method handler**

```go
func (c *ControlServer) handleRPC(w http.ResponseWriter, r *http.Request) {
    // ... existing auth and parsing

    switch req.Method {
    case "CustomMethod":
        var params customMethodParams
        if err := json.Unmarshal(req.Params, &params); err != nil {
            writeJSON(w, http.StatusOK, rpcResponse{Ok: false, Error: "Invalid params"})
            return
        }
        result := c.handleCustomMethod(params)
        writeJSON(w, http.StatusOK, rpcResponse{Ok: true, Result: result})
    // ... other cases
    }
}

func (c *ControlServer) handleCustomMethod(params customMethodParams) customMethodResponse {
    // Implement logic
    return customMethodResponse{Result: "success"}
}
```

**Step 3: Update web UI**

If method should be called from web UI, update [web/src/api.ts](../web/src/api.ts):

```typescript
export async function callCustomMethod(param1: string, param2: number): Promise<string> {
  const response = await fetch('/rpc', {
    method: 'POST',
    headers: {
      'Authorization': `Bearer ${getAuthToken()}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      jsonrpc: '2.0',
      method: 'CustomMethod',
      params: { param1, param2 },
      id: Date.now(),
    }),
  });
  const data = await response.json();
  if (!data.ok) throw new Error(data.error);
  return data.result.result;
}
```

**Step 4: Document in API reference**

Add method to Section 5.2.2 in [docs/api-reference.md](api-reference.md).

### Adding new metrics

Prometheus metrics are defined in `Metrics` struct in [internal/metrics/metrics.go](../internal/metrics/metrics.go).

**Step 1: Define metric**

```go
type Metrics struct {
    // ... existing metrics
    customCounter *prometheus.CounterVec
}

func NewMetrics(upstreamTags []string) *Metrics {
    customCounter := prometheus.NewCounterVec(prometheus.CounterOpts{
        Name: "fbforward_custom_total",
        Help: "Total custom events",
    }, []string{"upstream", "event_type"})

    prometheus.MustRegister(customCounter)

    return &Metrics{
        customCounter: customCounter,
    }
}
```

**Step 2: Add update method**

```go
func (m *Metrics) IncrementCustom(upstream string, eventType string) {
    m.customCounter.WithLabelValues(upstream, eventType).Inc()
}
```

**Step 3: Call from relevant code**

Example in forwarding code:

```go
l.metrics.IncrementCustom(up.Tag, "some_event")
```

**Step 4: Document metric**

Add to Section 5.2.4 metric catalog in [docs/api-reference.md](api-reference.md).

**Metric naming conventions:**

- Prefix: `fbforward_` (or `bwprobe_` for bwprobe metrics)
- Counters: `_total` suffix (e.g., `fbforward_bytes_up_total`)
- Gauges: No suffix (e.g., `fbforward_tcp_active`)
- Histograms: `_bucket`, `_sum`, `_count` suffixes (auto-generated by Prometheus client)
- Label names: Lowercase with underscores (e.g., `upstream`, `protocol`, `direction`)

---

## 7.3 Contributing

### Development setup

**Prerequisites:**

- Linux OS (required for platform-specific features)
- Go 1.25.5 or later
- Node.js 18+ and npm (for web UI development)
- Make (optional, for convenience targets)

**Clone repository:**

```bash
git clone https://github.com/NodePath81/fbforward.git
cd fbforward
```

**Install dependencies:**

```bash
go mod download
cd web && npm install && cd ..
```

**Build all binaries:**

```bash
make build
```

This builds:
- `build/bin/fbforward` (with embedded UI)
- `build/bin/bwprobe`
- `build/bin/fbmeasure`

**Build individual components:**

```bash
make build-fbforward  # Builds UI first, then Go binary
make build-bwprobe
make build-fbmeasure
```

**Development workflow for web UI:**

```bash
cd web
npm run dev  # Starts Vite dev server on http://localhost:5173
```

Hot reload is enabled. API calls proxy to fbforward running on `localhost:8080` (configure in `vite.config.ts`).

To build UI for embedding:

```bash
cd web
npm run build  # Outputs to web/dist/
```

**Set capabilities for testing:**

```bash
sudo setcap cap_net_raw+ep build/bin/fbforward
```

This allows ICMP probing without root.

**Run fbforward locally:**

```bash
cp configs/config.example.yaml config.yaml
# Edit config.yaml to configure upstreams and listeners
./build/bin/fbforward --config config.yaml
```

**Run fbmeasure on test upstream:**

On upstream host:

```bash
./build/bin/fbmeasure --port 9876
```

**Run bwprobe standalone test:**

```bash
./build/bin/bwprobe -target <upstream-host>:9876 -bandwidth 10m -samples 5
```

### Testing requirements

**Run all tests:**

```bash
make test
```

Or directly:

```bash
go test ./...
```

**Run tests for specific package:**

```bash
go test ./internal/upstream -v
go test ./bwprobe/internal/network -run TestTCPPayloadSize
```

**Test coverage:**

```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

**Testing guidelines:**

- Write tests for new packages in `<package>_test.go`
- Use table-driven tests for multiple input cases
- Mock external dependencies (network calls, file I/O)
- Test error conditions (invalid input, timeouts, nil pointers)
- Avoid tests requiring root or network access in CI

**Example test structure:**

```go
func TestFunctionName(t *testing.T) {
    tests := []struct {
        name     string
        input    InputType
        expected OutputType
        wantErr  bool
    }{
        {"valid input", validInput, validOutput, false},
        {"invalid input", invalidInput, nil, true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result, err := FunctionName(tt.input)
            if (err != nil) != tt.wantErr {
                t.Fatalf("expected error %v, got %v", tt.wantErr, err)
            }
            if !reflect.DeepEqual(result, tt.expected) {
                t.Errorf("expected %v, got %v", tt.expected, result)
            }
        })
    }
}
```

### Code style guidelines

**Go code:**

- Follow [Effective Go](https://go.dev/doc/effective_go) and [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)
- Use `gofmt` for formatting (enforced by `go fmt ./...`)
- Use meaningful variable names (avoid single-letter except loop indices)
- Exported functions and types require doc comments
- Error messages: Lowercase, no punctuation, descriptive context
- Avoid naked returns, named return values only for documentation
- Prefer table-driven tests

**Naming conventions:**

- Packages: Lowercase, singular, no underscores (e.g., `forwarding`, `upstream`)
- Interfaces: Noun or agent noun (e.g., `Closer`, `Logger`)
- Functions: Verb or verb phrase (e.g., `SelectUpstream`, `ComputeScore`)
- Constants: CamelCase (e.g., `defaultTCPTimeout`, not `DEFAULT_TCP_TIMEOUT`)

**TypeScript code (web UI):**

- Use TypeScript strict mode
- Define types for all API responses and config structures
- Use functional components with hooks (React)
- Format with Prettier (configured in `web/.prettierrc`)
- Follow [Airbnb React/JSX Style Guide](https://github.com/airbnb/javascript/tree/master/react)

**Documentation:**

- Follow [docs/style-guide.md](style-guide.md) for all documentation
- Use sentence case for headings
- Include code examples with language specification
- Cross-reference related sections with relative links
- Define terms on first use, link to [docs/glossary.md](glossary.md)

### Pull request process

**Before submitting:**

1. Run tests: `make test`
2. Format code: `go fmt ./...`
3. Build all binaries: `make build`
4. Test changes manually with local fbforward/bwprobe instances
5. Update documentation if behavior or configuration changed

**Commit message format:**

```
<type>: <short summary>

<optional detailed description>

<optional footer>
```

Types: `feat`, `fix`, `docs`, `refactor`, `test`, `chore`

Examples:
- `feat: add SCTP forwarding support`
- `fix: correct utilization penalty calculation`
- `docs: update configuration reference for shaping`
- `refactor: extract scoring logic to separate function`

**Pull request checklist:**

- [ ] Tests pass locally
- [ ] Code follows style guidelines
- [ ] Documentation updated (if applicable)
- [ ] Commit messages are descriptive
- [ ] No debug code or commented-out code
- [ ] No breaking changes to public API (or clearly noted)

**Review process:**

1. Submit PR against `main` branch
2. Automated checks run (build, test)
3. Maintainer reviews code
4. Address feedback, push updates
5. Maintainer merges when approved

**Breaking changes:**

If changes break backward compatibility (configuration schema, API, CLI flags), note this in PR description and commit message:

```
feat: redesign scoring algorithm

BREAKING CHANGE: Configuration field `scoring.weights` now requires
`protocol_blend` sub-field. Existing configs must be updated.
```

**Adding dependencies:**

If PR adds new Go or npm dependencies:
1. Justify need in PR description
2. Prefer standard library over third-party when possible
3. Check license compatibility (prefer MIT, BSD, Apache 2.0)
4. Run `go mod tidy` and commit `go.mod` and `go.sum` changes
