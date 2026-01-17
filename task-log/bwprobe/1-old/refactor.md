# BWProbe Project Refactoring Plan

## Executive Summary

This document outlines a comprehensive refactoring plan to improve the bwprobe project structure. The key objectives are:

1. **Expose Public APIs** - Allow other Go programs to import and use network quality measurement capabilities
2. **Clean Package Structure** - Reorganize internal packages and expose clean public interfaces
3. **Remove Clutter** - Delete build artifacts and consolidate documentation
4. **Add Testing** - Establish comprehensive test coverage

## Current State Assessment

### Project Statistics
- **Total Go files**: 16 files, 2,368 lines of code
- **Documentation files**: 15+ markdown files scattered across root and doc/ directories
- **Build artifacts**: Compiled binary and 110MB .gocache directory in repository
- **Test coverage**: 0% (no test files exist)
- **Package structure**: 10 internal packages (not importable by external programs)
- **Public API**: None - everything is in `internal/`

### Critical Issue: No Public API

**Current limitation**: All code is in `internal/` packages, which cannot be imported by external Go programs. The project is CLI-only with no programmatic interface.

**Required change**: Expose public APIs for:
- Sample-based bandwidth testing (TCP/UDP)
- RTT measurement with jitter calculation
- Loss and retransmission tracking
- Configurable test parameters (samples, bandwidth cap, byte limits)

## Required Public API Design

### API Requirements

External Go programs must be able to:

1. **Run sample-based quality tests** with configurable:
   - Network protocol (TCP/UDP)
   - Bandwidth cap
   - Number of samples
   - Bytes per sample
   - Wait time between samples
   - Maximum test duration

2. **Measure RTT and jitter** with:
   - Configurable sample rate
   - Min/mean/max RTT reporting
   - Jitter (standard deviation) calculation

3. **Track loss metrics**:
   - TCP: retransmit rate from TCP_INFO
   - UDP: sequence-based loss detection

4. **Receive structured results** including:
   - Throughput (bps)
   - RTT statistics (min/mean/max/jitter)
   - Loss/retransmit rates
   - Test duration and bytes transferred

### Proposed Public Package Structure

```
bwprobe/
├── probe/                      (PUBLIC API - importable by external programs)
│   ├── probe.go               (main API entry points)
│   ├── config.go              (configuration types)
│   ├── results.go             (result types)
│   ├── sampler.go             (sample-based testing API)
│   ├── rtt.go                 (RTT measurement API)
│   └── probe_test.go          (public API tests)
├── cmd/
│   └── bwprobe/
│       └── main.go            (CLI tool using public API)
├── internal/                   (PRIVATE implementation details)
│   ├── engine/                (test execution engine)
│   ├── network/               (network I/O)
│   ├── metrics/               (metric collection)
│   └── protocol/              (wire protocol)
└── examples/                   (usage examples)
    ├── simple_test.go
    ├── rtt_only.go
    └── custom_samples.go
```

## Concrete Changes Required

### Change 1: Create Public API Package

#### 1.1 Create `probe/config.go`
Define public configuration types:

```go
package probe

// Config defines parameters for a network quality test
type Config struct {
    Target       string        // Target host
    Port         int           // Target port
    Network      string        // "tcp" or "udp"
    BandwidthBps int64         // Bandwidth cap in bits per second
    Samples      int           // Number of samples to send
    SampleBytes  int64         // Bytes to send per sample
    Wait         time.Duration // Wait time between samples
    MaxDuration  time.Duration // Maximum test duration (0 = unlimited)
    RTTRate      int           // RTT samples per second
    ChunkSize    int           // Chunk size for sending
}

// RTTConfig defines parameters for RTT-only measurement
type RTTConfig struct {
    Target  string        // Target host
    Port    int           // Target port
    Network string        // "tcp" or "udp"
    Samples int           // Number of RTT samples
    Rate    int           // Samples per second
    Timeout time.Duration // Per-sample timeout
}
```

#### 1.2 Create `probe/results.go`
Define public result types:

```go
package probe

// Results contains the complete test results
type Results struct {
    Throughput    Throughput    // Throughput measurements
    RTT           RTTStats      // RTT statistics
    Loss          LossStats     // Loss/retransmit statistics
    TestDuration  time.Duration // Actual test duration
    BytesSent     int64         // Total bytes sent
    BytesReceived int64         // Total bytes received
}

// Throughput contains bandwidth measurements
type Throughput struct {
    Bps       int64   // Bits per second
    MeanMbps  float64 // Mean in Mbps
    Utilization float64 // Percentage of bandwidth cap used
}

// RTTStats contains RTT measurements
type RTTStats struct {
    Min    time.Duration // Minimum RTT
    Mean   time.Duration // Mean RTT
    Max    time.Duration // Maximum RTT
    Jitter time.Duration // Jitter (standard deviation)
    Samples int          // Number of RTT samples collected
}

// LossStats contains loss/retransmit statistics
type LossStats struct {
    Protocol       string  // "tcp" or "udp"
    Retransmits    uint32  // TCP: retransmit count
    SegmentsSent   uint32  // TCP: segments sent
    PacketsLost    uint32  // UDP: packets lost
    PacketsTotal   uint32  // UDP: total packets
    LossRate       float64 // Loss rate (0.0 to 1.0)
}
```

#### 1.3 Create `probe/probe.go`
Main API entry points:

```go
package probe

// Run executes a complete network quality test
func Run(ctx context.Context, cfg Config) (*Results, error)

// RunWithProgress executes a test with progress callbacks
func RunWithProgress(ctx context.Context, cfg Config, progress ProgressFunc) (*Results, error)

// ProgressFunc is called periodically during test execution
type ProgressFunc func(phase string, percentComplete float64, status string)
```

#### 1.4 Create `probe/sampler.go`
Sample-based testing API:

```go
package probe

// Sampler provides low-level control over sample-based testing
type Sampler struct {
    config Config
    conn   net.Conn
}

// NewSampler creates a new sampler instance
func NewSampler(cfg Config) (*Sampler, error)

// Connect establishes connection to target
func (s *Sampler) Connect(ctx context.Context) error

// SendSample sends a single sample of data
func (s *Sampler) SendSample(ctx context.Context, bytes int64) error

// GetMetrics retrieves current metrics from server
func (s *Sampler) GetMetrics(ctx context.Context) (*Results, error)

// Close closes the connection
func (s *Sampler) Close() error
```

#### 1.5 Create `probe/rtt.go`
RTT measurement API:

```go
package probe

// MeasureRTT performs RTT-only measurement
func MeasureRTT(ctx context.Context, cfg RTTConfig) (*RTTStats, error)

// RTTMeasurer provides continuous RTT monitoring
type RTTMeasurer struct {
    config RTTConfig
}

// NewRTTMeasurer creates a new RTT measurer
func NewRTTMeasurer(cfg RTTConfig) *RTTMeasurer

// Start begins RTT sampling in background
func (r *RTTMeasurer) Start(ctx context.Context) error

// GetStats returns current RTT statistics
func (r *RTTMeasurer) GetStats() *RTTStats

// Stop stops RTT sampling
func (r *RTTMeasurer) Stop()
```

### Change 2: Restructure Project Layout

#### 2.1 New Directory Structure

```
bwprobe/
├── probe/                      (PUBLIC - importable API)
│   ├── probe.go               (main API: Run, RunWithProgress)
│   ├── config.go              (Config, RTTConfig types)
│   ├── results.go             (Results, RTTStats, LossStats types)
│   ├── sampler.go             (Sampler for low-level control)
│   ├── rtt.go                 (RTT measurement API)
│   ├── probe_test.go
│   ├── sampler_test.go
│   └── rtt_test.go
├── cmd/
│   └── bwprobe/
│       └── main.go            (CLI using probe package)
├── internal/
│   ├── engine/                (test execution implementation)
│   │   ├── runner.go          (from client/client.go)
│   │   ├── samples.go         (from client/samples.go)
│   │   └── control.go         (from client/control.go)
│   ├── network/               (network layer)
│   │   ├── sender.go          (from client/sender.go)
│   │   ├── ratelimit.go       (from ratelimit/)
│   │   └── conn.go            (connection helpers)
│   ├── metrics/               (metrics collection)
│   │   ├── tcp.go             (from tcpinfo/)
│   │   ├── udp.go             (from udpseq/)
│   │   ├── rtt.go             (from sampling/)
│   │   └── aggregation.go     (stats aggregation)
│   ├── protocol/              (wire protocol)
│   │   ├── types.go           (from types/)
│   │   └── messages.go        (protocol messages)
│   ├── server/
│   │   └── server.go          (server implementation)
│   └── util/
│       ├── parse.go           (from utils/parse.go)
│       └── format.go          (from utils/format.go)
├── examples/
│   ├── simple_test.go         (basic usage example)
│   ├── rtt_only.go            (RTT-only measurement)
│   ├── custom_samples.go      (low-level sampler usage)
│   └── concurrent.go          (concurrent tests)
├── docs/
│   ├── API.md                 (public API documentation)
│   ├── ARCHITECTURE.md        (system design)
│   ├── ALGORITHM.md           (measurement algorithms)
│   └── DEVELOPMENT.md         (development guide)
└── test/
    ├── integration/
    │   ├── api_test.go        (public API integration tests)
    │   ├── tcp_test.go
    │   └── udp_test.go
    └── testdata/
```

#### 2.2 Move CLI to cmd/ Directory

Current `main.go` → `cmd/bwprobe/main.go`:

```go
package main

import (
    "context"
    "flag"
    "fmt"
    "os"

    "bwprobe/probe"
    "bwprobe/internal/server"
    "bwprobe/internal/util"
)

func main() {
    // Parse flags
    mode := flag.String("mode", "client", "Mode: server or client")
    // ... other flags ...
    flag.Parse()

    if *mode == "server" {
        // Run server
        server.Run(serverConfig)
        return
    }

    // Build probe.Config from flags
    cfg := probe.Config{
        Target:       *target,
        Port:         *port,
        Network:      *network,
        BandwidthBps: bwBps,
        Samples:      *samples,
        SampleBytes:  sampleBytes,
        Wait:         *wait,
        MaxDuration:  *maxDuration,
        RTTRate:      *rttRate,
        ChunkSize:    chunkSize,
    }

    // Run test using public API
    ctx := context.Background()
    results, err := probe.RunWithProgress(ctx, cfg, printProgress)
    if err != nil {
        fmt.Fprintln(os.Stderr, "error:", err)
        os.Exit(1)
    }

    // Print results
    printResults(results)
}
```

Update `go.mod`:
```
module bwprobe

go 1.25.5

require golang.org/x/sys v0.37.0
```

Update build in `Makefile`:
```makefile
build:
	$(GO) build -o $(BINARY) ./cmd/bwprobe
```

### Change 3: Code Reorganization

#### 3.1 Files to Move/Refactor

| Current Location | New Location | Refactoring Required |
|-----------------|--------------|---------------------|
| `internal/client/client.go` | `probe/probe.go` + `internal/engine/runner.go` | Split: public API → probe/, implementation → internal/engine/ |
| `internal/client/samples.go` | `internal/engine/samples.go` | Move as-is |
| `internal/client/sender.go` | `internal/network/sender.go` | Move as-is |
| `internal/client/control.go` | `internal/engine/control.go` | Move as-is |
| `internal/client/ping.go` | `internal/metrics/rtt.go` | Merge with sampling/rtt.go |
| `internal/client/progress_tracker.go` | `internal/engine/progress.go` | Move, keep internal |
| `internal/sampling/rtt.go` | `internal/metrics/rtt.go` | Merge with client/ping.go |
| `internal/tcpinfo/tcpinfo.go` | `internal/metrics/tcp.go` | Move as-is |
| `internal/udpseq/sequence.go` | `internal/metrics/udp.go` | Move as-is |
| `internal/ratelimit/ratelimit.go` | `internal/network/ratelimit.go` | Move as-is |
| `internal/types/types.go` | `internal/protocol/types.go` + `probe/results.go` | Split: wire types → protocol/, API types → probe/ |
| `internal/progress/progress.go` | Keep in internal (CLI only) | No change, not part of public API |
| `internal/utils/parse.go` | `internal/util/parse.go` | Move as-is |
| `internal/utils/format.go` | `internal/util/format.go` | Move as-is |
| `internal/server/server.go` | Keep in `internal/server/` | No change |
| `main.go` | `cmd/bwprobe/main.go` | Move and refactor to use public API |

#### 3.2 Create New Files

**New Public API Files** (in `probe/`):
1. `probe/probe.go` - Main API functions (`Run`, `RunWithProgress`)
2. `probe/config.go` - Configuration types (`Config`, `RTTConfig`)
3. `probe/results.go` - Result types (`Results`, `RTTStats`, `LossStats`, `Throughput`)
4. `probe/sampler.go` - Low-level sampler API (`Sampler` type and methods)
5. `probe/rtt.go` - RTT measurement API (`MeasureRTT`, `RTTMeasurer`)
6. `probe/errors.go` - Error types and constants
7. `probe/doc.go` - Package documentation

**New Internal Files**:
1. `internal/protocol/messages.go` - Wire protocol message definitions
2. `internal/metrics/aggregation.go` - Statistics aggregation functions
3. `internal/network/conn.go` - Connection helper functions

**Example Files** (in `examples/`):
1. `examples/simple_test.go` - Basic test example
2. `examples/rtt_only.go` - RTT-only measurement
3. `examples/custom_samples.go` - Custom sample-based testing
4. `examples/concurrent.go` - Multiple concurrent tests

**Documentation Files** (in `docs/`):
1. `docs/API.md` - Public API reference
2. `docs/ARCHITECTURE.md` - High-level architecture
3. `docs/ALGORITHM.md` - Measurement algorithms (consolidate algorithm.md + OPTIMIZATION_SUMMARY.md)
4. `docs/DEVELOPMENT.md` - Development and contributing guide

### Change 4: Implementation Details

#### 4.1 Public API Implementation (`probe/probe.go`)

```go
package probe

import (
    "context"
    "fmt"

    "bwprobe/internal/engine"
    "bwprobe/internal/metrics"
)

// Run executes a complete network quality test
func Run(ctx context.Context, cfg Config) (*Results, error) {
    return RunWithProgress(ctx, cfg, nil)
}

// RunWithProgress executes a test with progress callbacks
func RunWithProgress(ctx context.Context, cfg Config, progress ProgressFunc) (*Results, error) {
    // Validate config
    if err := cfg.Validate(); err != nil {
        return nil, fmt.Errorf("invalid config: %w", err)
    }

    // Create internal runner (from internal/engine)
    runner, err := engine.NewRunner(cfg.toInternal())
    if err != nil {
        return nil, err
    }
    defer runner.Close()

    // Set up progress callback
    if progress != nil {
        runner.SetProgressCallback(func(phase string, pct float64, status string) {
            progress(phase, pct, status)
        })
    }

    // Execute test
    internalResults, err := runner.Run(ctx)
    if err != nil {
        return nil, err
    }

    // Convert internal results to public API format
    return resultsFromInternal(internalResults), nil
}

// toInternal converts public Config to internal engine config
func (c Config) toInternal() engine.Config {
    return engine.Config{
        Target:       c.Target,
        Port:         c.Port,
        Network:      c.Network,
        BandwidthBps: c.BandwidthBps,
        Samples:      c.Samples,
        SampleBytes:  c.SampleBytes,
        Wait:         c.Wait,
        MaxDuration:  c.MaxDuration,
        RTTRate:      c.RTTRate,
        ChunkSize:    c.ChunkSize,
    }
}

// resultsFromInternal converts internal results to public Results type
func resultsFromInternal(r *engine.Results) *Results {
    return &Results{
        Throughput: Throughput{
            Bps:         r.ThroughputBps,
            MeanMbps:    float64(r.ThroughputBps) / 1_000_000,
            Utilization: r.Utilization,
        },
        RTT: RTTStats{
            Min:     r.RTTMin,
            Mean:    r.RTTMean,
            Max:     r.RTTMax,
            Jitter:  r.RTTJitter,
            Samples: r.RTTSamples,
        },
        Loss: LossStats{
            Protocol:     r.Protocol,
            Retransmits:  r.TCPRetransmits,
            SegmentsSent: r.TCPSegmentsSent,
            PacketsLost:  r.UDPPacketsLost,
            PacketsTotal: r.UDPPacketsTotal,
            LossRate:     r.LossRate,
        },
        TestDuration:  r.Duration,
        BytesSent:     r.BytesSent,
        BytesReceived: r.BytesReceived,
    }
}
```

#### 4.2 Sampler API Implementation (`probe/sampler.go`)

```go
package probe

import (
    "context"
    "net"

    "bwprobe/internal/network"
    "bwprobe/internal/engine"
)

// Sampler provides low-level control over sample-based testing
type Sampler struct {
    config   Config
    conn     net.Conn
    control  *engine.ControlConn
    sender   *network.Sender
}

// NewSampler creates a new sampler instance
func NewSampler(cfg Config) (*Sampler, error) {
    if err := cfg.Validate(); err != nil {
        return nil, err
    }

    return &Sampler{
        config: cfg,
    }, nil
}

// Connect establishes connection to target
func (s *Sampler) Connect(ctx context.Context) error {
    // Establish data connection
    conn, err := net.DialTimeout(s.config.Network,
        fmt.Sprintf("%s:%d", s.config.Target, s.config.Port),
        10*time.Second)
    if err != nil {
        return err
    }
    s.conn = conn

    // Establish control connection
    s.control, err = engine.NewControlConn(s.config.Target, s.config.Port)
    if err != nil {
        s.conn.Close()
        return err
    }

    // Create sender
    s.sender = network.NewSender(s.conn, s.config.BandwidthBps, s.config.ChunkSize)

    return nil
}

// SendSample sends a single sample of data
func (s *Sampler) SendSample(ctx context.Context, bytes int64) error {
    return s.sender.Send(ctx, bytes)
}

// GetMetrics retrieves current metrics from server
func (s *Sampler) GetMetrics(ctx context.Context) (*Results, error) {
    stats, err := s.control.GetStats(ctx)
    if err != nil {
        return nil, err
    }
    return resultsFromStats(stats), nil
}

// Close closes the connection
func (s *Sampler) Close() error {
    if s.conn != nil {
        s.conn.Close()
    }
    if s.control != nil {
        s.control.Close()
    }
    return nil
}
```

#### 4.3 RTT API Implementation (`probe/rtt.go`)

```go
package probe

import (
    "context"
    "sync"

    "bwprobe/internal/metrics"
)

// MeasureRTT performs RTT-only measurement
func MeasureRTT(ctx context.Context, cfg RTTConfig) (*RTTStats, error) {
    if err := cfg.Validate(); err != nil {
        return nil, err
    }

    measurer := metrics.NewRTTMeasurer(cfg.Target, cfg.Port, cfg.Network)
    samples, err := measurer.Measure(ctx, cfg.Samples, cfg.Rate)
    if err != nil {
        return nil, err
    }

    return &RTTStats{
        Min:     samples.Min,
        Mean:    samples.Mean,
        Max:     samples.Max,
        Jitter:  samples.StdDev,
        Samples: len(samples.Durations),
    }, nil
}

// RTTMeasurer provides continuous RTT monitoring
type RTTMeasurer struct {
    config   RTTConfig
    measurer *metrics.RTTMeasurer
    cancel   context.CancelFunc
    wg       sync.WaitGroup
    mu       sync.RWMutex
    stats    RTTStats
}

// NewRTTMeasurer creates a new RTT measurer
func NewRTTMeasurer(cfg RTTConfig) *RTTMeasurer {
    return &RTTMeasurer{
        config:   cfg,
        measurer: metrics.NewRTTMeasurer(cfg.Target, cfg.Port, cfg.Network),
    }
}

// Start begins RTT sampling in background
func (r *RTTMeasurer) Start(ctx context.Context) error {
    ctx, cancel := context.WithCancel(ctx)
    r.cancel = cancel

    r.wg.Add(1)
    go func() {
        defer r.wg.Done()
        r.measureLoop(ctx)
    }()

    return nil
}

// GetStats returns current RTT statistics
func (r *RTTMeasurer) GetStats() *RTTStats {
    r.mu.RLock()
    defer r.mu.RUnlock()
    return &r.stats
}

// Stop stops RTT sampling
func (r *RTTMeasurer) Stop() {
    if r.cancel != nil {
        r.cancel()
        r.wg.Wait()
    }
}

func (r *RTTMeasurer) measureLoop(ctx context.Context) {
    ticker := time.NewTicker(time.Second / time.Duration(r.config.Rate))
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            samples, err := r.measurer.Measure(ctx, 1, r.config.Rate)
            if err != nil {
                continue
            }

            r.mu.Lock()
            // Update running statistics
            r.updateStats(samples)
            r.mu.Unlock()
        }
    }
}
```

### Change 5: Example Programs

#### 5.1 Create `examples/simple_test.go`

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "bwprobe/probe"
)

func main() {
    cfg := probe.Config{
        Target:       "example.com",
        Port:         9999,
        Network:      "tcp",
        BandwidthBps: 100_000_000, // 100 Mbps
        Samples:      10,
        SampleBytes:  20_000_000,  // 20 MB per sample
        Wait:         100 * time.Millisecond,
        MaxDuration:  30 * time.Second,
        RTTRate:      10,
        ChunkSize:    65536,
    }

    ctx := context.Background()
    results, err := probe.Run(ctx, cfg)
    if err != nil {
        log.Fatalf("Test failed: %v", err)
    }

    fmt.Printf("Throughput: %.2f Mbps\n", results.Throughput.MeanMbps)
    fmt.Printf("RTT: min=%v mean=%v max=%v jitter=%v\n",
        results.RTT.Min, results.RTT.Mean, results.RTT.Max, results.RTT.Jitter)
    fmt.Printf("Loss rate: %.2f%%\n", results.Loss.LossRate*100)
}
```

#### 5.2 Create `examples/rtt_only.go`

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "bwprobe/probe"
)

func main() {
    cfg := probe.RTTConfig{
        Target:  "example.com",
        Port:    9999,
        Network: "tcp",
        Samples: 50,
        Rate:    10, // 10 samples per second
        Timeout: 5 * time.Second,
    }

    ctx := context.Background()
    stats, err := probe.MeasureRTT(ctx, cfg)
    if err != nil {
        log.Fatalf("RTT measurement failed: %v", err)
    }

    fmt.Printf("RTT Statistics (%d samples):\n", stats.Samples)
    fmt.Printf("  Min:    %v\n", stats.Min)
    fmt.Printf("  Mean:   %v\n", stats.Mean)
    fmt.Printf("  Max:    %v\n", stats.Max)
    fmt.Printf("  Jitter: %v\n", stats.Jitter)
}
```

#### 5.3 Create `examples/custom_samples.go`

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "bwprobe/probe"
)

func main() {
    cfg := probe.Config{
        Target:       "example.com",
        Port:         9999,
        Network:      "tcp",
        BandwidthBps: 50_000_000, // 50 Mbps
        ChunkSize:    65536,
    }

    sampler, err := probe.NewSampler(cfg)
    if err != nil {
        log.Fatalf("Failed to create sampler: %v", err)
    }
    defer sampler.Close()

    ctx := context.Background()
    if err := sampler.Connect(ctx); err != nil {
        log.Fatalf("Failed to connect: %v", err)
    }

    // Custom sampling: send varying amounts
    sampleSizes := []int64{10_000_000, 20_000_000, 30_000_000}

    for i, size := range sampleSizes {
        fmt.Printf("Sending sample %d (%d bytes)...\n", i+1, size)

        if err := sampler.SendSample(ctx, size); err != nil {
            log.Fatalf("Failed to send sample: %v", err)
        }

        time.Sleep(500 * time.Millisecond)
    }

    // Get final results
    results, err := sampler.GetMetrics(ctx)
    if err != nil {
        log.Fatalf("Failed to get metrics: %v", err)
    }

    fmt.Printf("\nResults:\n")
    fmt.Printf("  Throughput: %.2f Mbps\n", results.Throughput.MeanMbps)
    fmt.Printf("  Bytes sent: %d\n", results.BytesSent)
    fmt.Printf("  Duration: %v\n", results.TestDuration)
}
```

### Change 6: Clean Up Project

#### 6.1 Remove Build Artifacts

```bash
rm bwprobe
rm main.go.backup
rm -rf .gocache/
```

#### 6.2 Update .gitignore

```gitignore
# Build artifacts
.gocache/
bwprobe
cmd/bwprobe/bwprobe

# Backup files
*.backup
*.old
*.tmp
*~

# Test artifacts
*.test
*.out
coverage.txt
coverage.html

# OS-specific
.DS_Store
Thumbs.db

# IDE
.vscode/
.idea/
*.swp
*.swo

# Claude
.claude/
```

#### 6.3 Consolidate Documentation

**Create `docs/` directory structure:**

```bash
mkdir -p docs/implementation docs/fixes docs/archive
```

**Move and consolidate files:**

```bash
# Consolidate algorithm docs
cat algorithm.md OPTIMIZATION_SUMMARY.md > docs/ALGORITHM.md

# Move implementation docs
mv doc/tcp-info-implementation.md docs/implementation/tcp-info.md
mv doc/BANDWIDTH_MEASUREMENT_ROOT_CAUSE.md docs/implementation/bandwidth-measurement.md
mv doc/THROUGHPUT_MEASUREMENT_FIX.md docs/implementation/throughput-fix.md

# Move bug fix docs
mv doc/RETRANSMIT_BUG_ANALYSIS.md docs/fixes/retransmit-bug.md
mv doc/PROGRESS_SPEED_CHANGING_FIX.md docs/fixes/progress-speed.md
mv doc/EBPF_REMOVAL_SUMMARY.md docs/fixes/ebpf-removal.md

# Archive old refactoring docs
mv REFACTORING.md docs/archive/refactoring-v1.md
# Consolidate doc/1/ and doc/2/ into archive

# Remove old docs
rm algorithm.md OPTIMIZATION_SUMMARY.md
rm -rf doc/
```

**Create new documentation:**

1. `docs/API.md` - Public API reference with usage examples
2. `docs/ARCHITECTURE.md` - System architecture and design decisions
3. `docs/DEVELOPMENT.md` - Development setup, testing, contributing

### Change 7: Testing Infrastructure

#### 7.1 Public API Tests (`probe/`)

Create test files for all public APIs:

- `probe/probe_test.go` - Test `Run()` and `RunWithProgress()`
- `probe/sampler_test.go` - Test `Sampler` API
- `probe/rtt_test.go` - Test RTT measurement APIs
- `probe/config_test.go` - Test configuration validation

#### 7.2 Internal Package Tests

Add test files for internal packages:

- `internal/engine/runner_test.go`
- `internal/network/sender_test.go`
- `internal/network/ratelimit_test.go`
- `internal/metrics/tcp_test.go`
- `internal/metrics/udp_test.go`
- `internal/metrics/rtt_test.go`
- `internal/util/parse_test.go`
- `internal/util/format_test.go`

#### 7.3 Integration Tests (`test/integration/`)

```
test/
├── integration/
│   ├── api_test.go         (test public API end-to-end)
│   ├── tcp_quality_test.go (TCP quality test scenarios)
│   ├── udp_quality_test.go (UDP quality test scenarios)
│   └── rtt_test.go         (RTT measurement scenarios)
└── testdata/
    └── fixtures/
```

### Change 8: Build System Updates

#### 8.1 Update Makefile

```makefile
.PHONY: all build clean test coverage lint fmt vet install examples

GO := go
BINARY := bwprobe
COVERAGE := coverage.txt

all: fmt vet test build

build:
	$(GO) build -o $(BINARY) ./cmd/bwprobe

clean:
	rm -f $(BINARY)
	rm -f cmd/bwprobe/$(BINARY)
	rm -f $(COVERAGE) coverage.html
	rm -f *.test
	rm -f examples/simple_test examples/rtt_only examples/custom_samples

test:
	$(GO) test ./... -v -race -timeout 30s

coverage:
	$(GO) test ./... -race -coverprofile=$(COVERAGE) -covermode=atomic
	$(GO) tool cover -html=$(COVERAGE) -o coverage.html

bench:
	$(GO) test ./... -bench=. -benchmem

lint:
	golangci-lint run

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

install:
	$(GO) install ./cmd/bwprobe

examples:
	$(GO) build -o examples/simple_test examples/simple_test.go
	$(GO) build -o examples/rtt_only examples/rtt_only.go
	$(GO) build -o examples/custom_samples examples/custom_samples.go

help:
	@echo "Available targets:"
	@echo "  build     - Build the CLI binary"
	@echo "  test      - Run unit tests"
	@echo "  coverage  - Generate coverage report"
	@echo "  bench     - Run benchmarks"
	@echo "  lint      - Run linter"
	@echo "  fmt       - Format code"
	@echo "  vet       - Run go vet"
	@echo "  examples  - Build example programs"
	@echo "  clean     - Remove build artifacts"
	@echo "  install   - Install CLI binary"
```

#### 8.2 Update Module Path (if needed)

Consider whether to keep `bwprobe` or use a more specific module name like `github.com/username/bwprobe`:

```
module github.com/username/bwprobe

go 1.25.5

require golang.org/x/sys v0.37.0
```

## Final Project Structure

```
bwprobe/
├── probe/                      ← PUBLIC API (importable)
│   ├── probe.go               (Run, RunWithProgress)
│   ├── config.go              (Config, RTTConfig)
│   ├── results.go             (Results, RTTStats, LossStats)
│   ├── sampler.go             (Sampler type)
│   ├── rtt.go                 (RTT APIs)
│   ├── errors.go              (Error types)
│   ├── doc.go                 (Package docs)
│   ├── probe_test.go
│   ├── sampler_test.go
│   ├── rtt_test.go
│   └── config_test.go
├── cmd/
│   └── bwprobe/
│       └── main.go            (CLI tool)
├── internal/                   ← PRIVATE implementation
│   ├── engine/
│   │   ├── runner.go
│   │   ├── samples.go
│   │   ├── control.go
│   │   ├── progress.go
│   │   └── runner_test.go
│   ├── network/
│   │   ├── sender.go
│   │   ├── ratelimit.go
│   │   ├── conn.go
│   │   ├── sender_test.go
│   │   └── ratelimit_test.go
│   ├── metrics/
│   │   ├── tcp.go
│   │   ├── udp.go
│   │   ├── rtt.go
│   │   ├── aggregation.go
│   │   ├── tcp_test.go
│   │   ├── udp_test.go
│   │   └── rtt_test.go
│   ├── protocol/
│   │   ├── types.go
│   │   └── messages.go
│   ├── server/
│   │   ├── server.go
│   │   └── server_test.go
│   └── util/
│       ├── parse.go
│       ├── format.go
│       ├── parse_test.go
│       └── format_test.go
├── examples/
│   ├── simple_test.go         (basic usage)
│   ├── rtt_only.go            (RTT measurement)
│   ├── custom_samples.go      (low-level API)
│   └── concurrent.go          (concurrent tests)
├── test/
│   ├── integration/
│   │   ├── api_test.go
│   │   ├── tcp_quality_test.go
│   │   └── udp_quality_test.go
│   └── testdata/
│       └── fixtures/
├── docs/
│   ├── API.md                 (public API docs)
│   ├── ARCHITECTURE.md        (design)
│   ├── ALGORITHM.md           (algorithms)
│   ├── DEVELOPMENT.md         (contributing)
│   ├── implementation/        (tech details)
│   ├── fixes/                 (bug fixes)
│   └── archive/               (historical)
├── .github/
│   └── workflows/
│       └── ci.yml
├── .editorconfig
├── .gitignore
├── LICENSE
├── README.md
├── CONTRIBUTING.md
├── Makefile
├── go.mod
└── go.sum
```

## Import Path Examples

External Go programs can now import and use bwprobe:

```go
import "bwprobe/probe"

// Run a complete test
results, err := probe.Run(ctx, probe.Config{
    Target:       "target.example.com",
    BandwidthBps: 100_000_000,
    Samples:      10,
    SampleBytes:  20_000_000,
})

// Measure RTT only
rttStats, err := probe.MeasureRTT(ctx, probe.RTTConfig{
    Target:  "target.example.com",
    Samples: 50,
})

// Low-level control
sampler, _ := probe.NewSampler(config)
sampler.Connect(ctx)
sampler.SendSample(ctx, 10_000_000)
results, _ := sampler.GetMetrics(ctx)
```

## Key Benefits

### 1. Importable by External Programs
- Public `probe/` package for external use
- Clean API surface with minimal dependencies
- Well-documented public types and functions

### 2. Backwards Compatible CLI
- CLI tool still works exactly the same
- Now uses public API internally
- Located in `cmd/bwprobe/`

### 3. Better Organization
- Clear separation: public API vs. internal implementation
- Fewer, more focused internal packages
- Examples demonstrate API usage

### 4. Testable
- Public API can be tested end-to-end
- Internal packages have unit tests
- Integration tests verify full workflows

### 5. Professional Standards
- Standard Go project layout (`cmd/`, `internal/`, `pkg/`)
- Follows Go best practices
- Ready for Go module distribution

## Success Criteria

- [ ] Public `probe/` package with complete API
- [ ] External programs can import and use bwprobe
- [ ] All APIs support TCP and UDP protocols
- [ ] RTT, jitter, throughput, and loss metrics exposed
- [ ] CLI tool works unchanged using public API
- [ ] All internal packages refactored and reorganized
- [ ] Comprehensive test coverage (>70%)
- [ ] Working examples in `examples/`
- [ ] Complete API documentation in `docs/API.md`
- [ ] Zero build artifacts in repository
- [ ] All code passes `go vet` and `go fmt`
- [ ] CI pipeline passing

## Implementation Order

1. **Create public API skeleton** - Define types in `probe/` package
2. **Refactor internal packages** - Reorganize code into new structure
3. **Implement public APIs** - Wire public functions to internal implementation
4. **Move CLI to cmd/** - Refactor main.go to use public API
5. **Add examples** - Create working example programs
6. **Write tests** - Add comprehensive test coverage
7. **Documentation** - Write API docs and update README
8. **Clean up** - Remove artifacts, consolidate docs
9. **Verify** - Test CLI, test examples, test public API imports
