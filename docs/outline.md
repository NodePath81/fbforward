# fbforward documentation outline

This outline defines the complete documentation structure for the fbforward monorepo. Each section includes its purpose, source artifacts, intended audience, and dependencies.

---

## 1. Project overview

### 1.1 Purpose and scope

| Field | Value |
|-------|-------|
| **ID** | 1.1 |
| **Purpose** | Explain what fbforward does and the problems it solves |
| **Source artifacts** | CLAUDE.md, README (if exists), cmd/fbforward/main.go |
| **Dependencies** | None |
| **Audience** | All readers |
| **Depth** | Overview |

**Content:**
- Problem statement: intelligent upstream selection for TCP/UDP forwarding
- Core capabilities: measurement-driven selection, flow pinning, traffic shaping
- Target use cases and deployment scenarios

### 1.2 Architecture overview

| Field | Value |
|-------|-------|
| **ID** | 1.2 |
| **Purpose** | Present the high-level system architecture and component relationships |
| **Source artifacts** | internal/app/runtime.go, internal/app/supervisor.go, CLAUDE.md |
| **Dependencies** | 1.1 |
| **Audience** | All readers |
| **Depth** | Overview |

**Content:**
- Three-plane design: data plane, control plane, measurement plane
- Component diagram showing major subsystems
- Binary relationships: fbforward, bwprobe, fbmeasure

### 1.3 Component relationships

| Field | Value |
|-------|-------|
| **ID** | 1.3 |
| **Purpose** | Describe how components interact at runtime |
| **Source artifacts** | internal/app/runtime.go, internal/upstream/upstream.go |
| **Dependencies** | 1.2 |
| **Audience** | Operators, developers |
| **Depth** | Overview |

**Content:**
- Startup sequence and component wiring
- Data flow between planes
- Shutdown and restart lifecycle

---

## 2. Getting started

### 2.1 Prerequisites

| Field | Value |
|-------|-------|
| **ID** | 2.1 |
| **Purpose** | List system requirements and dependencies |
| **Source artifacts** | go.mod, Makefile, CLAUDE.md |
| **Dependencies** | None |
| **Audience** | Users, operators |
| **Depth** | Reference |

**Content:**
- Linux requirement (kernel features: SO_MAX_PACING_RATE, TCP_INFO, raw ICMP)
- Go version requirement (1.25.5+)
- Required capabilities (CAP_NET_RAW, CAP_NET_ADMIN)
- Node.js for web UI development

### 2.2 Installation

| Field | Value |
|-------|-------|
| **ID** | 2.2 |
| **Purpose** | Guide users through installation options |
| **Source artifacts** | Makefile, deploy/debian/, deploy/systemd/ |
| **Dependencies** | 2.1 |
| **Audience** | Users, operators |
| **Depth** | Reference |

**Content:**
- Building from source (make build)
- Debian package installation
- systemd service setup
- Setting capabilities

### 2.3 Quick start

| Field | Value |
|-------|-------|
| **ID** | 2.3 |
| **Purpose** | Provide minimal steps to get fbforward running |
| **Source artifacts** | configs/config.example.yaml, cmd/fbforward/main.go |
| **Dependencies** | 2.2 |
| **Audience** | Users |
| **Depth** | Overview |

**Content:**
- Minimal configuration example
- Starting fbmeasure on upstreams
- Starting fbforward
- Verifying operation via web UI

---

## 3. User guides

### 3.1 fbforward

#### 3.1.1 Overview

| Field | Value |
|-------|-------|
| **ID** | 3.1.1 |
| **Purpose** | Introduce fbforward binary and its operational modes |
| **Source artifacts** | cmd/fbforward/main.go, CLAUDE.md |
| **Dependencies** | 2.3 |
| **Audience** | Users, operators |
| **Depth** | Overview |

**Content:**
- What fbforward does (TCP/UDP forwarding with intelligent upstream selection)
- Operational modes (auto selection, manual selection)
- Flow pinning semantics

#### 3.1.2 Configuration

| Field | Value |
|-------|-------|
| **ID** | 3.1.2 |
| **Purpose** | Document all configuration options for fbforward |
| **Source artifacts** | internal/config/config.go, configs/config.example.yaml |
| **Dependencies** | 3.1.1 |
| **Audience** | Operators |
| **Depth** | Reference |

**Content:**
- Configuration file format and loading
- Section-by-section reference (cross-reference to Section 4)
- Environment variable overrides
- Validation rules

#### 3.1.3 Operation

| Field | Value |
|-------|-------|
| **ID** | 3.1.3 |
| **Purpose** | Describe day-to-day operational procedures |
| **Source artifacts** | internal/control/control.go, internal/app/supervisor.go |
| **Dependencies** | 3.1.2 |
| **Audience** | Operators |
| **Depth** | Reference |

**Content:**
- Starting and stopping
- Hot reload / restart via RPC
- Monitoring via web UI and Prometheus metrics
- Log interpretation

#### 3.1.4 Troubleshooting

| Field | Value |
|-------|-------|
| **ID** | 3.1.4 |
| **Purpose** | Help operators diagnose and resolve common issues |
| **Source artifacts** | internal/ error handling patterns, logs |
| **Dependencies** | 3.1.3 |
| **Audience** | Operators |
| **Depth** | Reference |

**Content:**
- Common error messages and causes
- Diagnostic checklist (capabilities, connectivity, measurement server)
- Performance troubleshooting
- Log analysis patterns

### 3.2 bwprobe

#### 3.2.1 Overview

| Field | Value |
|-------|-------|
| **ID** | 3.2.1 |
| **Purpose** | Introduce bwprobe CLI tool and its measurement model |
| **Source artifacts** | bwprobe/cmd/main.go, bwprobe/pkg/doc.go |
| **Dependencies** | 2.3 |
| **Audience** | Users, operators |
| **Depth** | Overview |

**Content:**
- What bwprobe measures (bandwidth, RTT, jitter, loss/retransmit)
- Two-channel design (control + data)
- Sample-based testing model
- Upload vs download (reverse mode)

#### 3.2.2 Configuration

| Field | Value |
|-------|-------|
| **ID** | 3.2.2 |
| **Purpose** | Document CLI flags and configuration options |
| **Source artifacts** | bwprobe/cmd/main.go, bwprobe/pkg/config.go |
| **Dependencies** | 3.2.1 |
| **Audience** | Users |
| **Depth** | Reference |

**Content:**
- CLI flag reference
- Target bandwidth and sample configuration
- Timeout settings
- Output format options

#### 3.2.3 Operation

| Field | Value |
|-------|-------|
| **ID** | 3.2.3 |
| **Purpose** | Describe how to run measurements and interpret results |
| **Source artifacts** | bwprobe/cmd/main.go, bwprobe/pkg/results.go |
| **Dependencies** | 3.2.2 |
| **Audience** | Users, operators |
| **Depth** | Reference |

**Content:**
- Running TCP and UDP tests
- Interpreting output metrics
- Trimmed mean and percentile calculations
- Comparing upload vs download performance

#### 3.2.4 Troubleshooting

| Field | Value |
|-------|-------|
| **ID** | 3.2.4 |
| **Purpose** | Help diagnose bwprobe measurement issues |
| **Source artifacts** | bwprobe/pkg/errors.go |
| **Dependencies** | 3.2.3 |
| **Audience** | Users, operators |
| **Depth** | Reference |

**Content:**
- Connection failures
- Timeout issues
- Measurement anomalies
- Server-side diagnostics

### 3.3 fbmeasure

#### 3.3.1 Overview

| Field | Value |
|-------|-------|
| **ID** | 3.3.1 |
| **Purpose** | Introduce fbmeasure measurement server |
| **Source artifacts** | bwprobe/cmd/fbmeasure/main.go |
| **Dependencies** | 2.3 |
| **Audience** | Operators |
| **Depth** | Overview |

**Content:**
- Purpose (measurement endpoint for bwprobe tests)
- Deployment requirements
- Relationship to fbforward

#### 3.3.2 Configuration

| Field | Value |
|-------|-------|
| **ID** | 3.3.2 |
| **Purpose** | Document fbmeasure configuration options |
| **Source artifacts** | bwprobe/cmd/fbmeasure/main.go |
| **Dependencies** | 3.3.1 |
| **Audience** | Operators |
| **Depth** | Reference |

**Content:**
- CLI flags (port, bind address)
- Firewall requirements

#### 3.3.3 Operation

| Field | Value |
|-------|-------|
| **ID** | 3.3.3 |
| **Purpose** | Describe deployment and monitoring procedures |
| **Source artifacts** | bwprobe/cmd/fbmeasure/main.go |
| **Dependencies** | 3.3.2 |
| **Audience** | Operators |
| **Depth** | Reference |

**Content:**
- Starting as a service
- Verifying connectivity
- Resource usage

---

## 4. Configuration reference

### 4.1 Configuration file format

| Field | Value |
|-------|-------|
| **ID** | 4.1 |
| **Purpose** | Describe YAML configuration structure and conventions |
| **Source artifacts** | internal/config/config.go, configs/config.example.yaml |
| **Dependencies** | 3.1.2 |
| **Audience** | Operators |
| **Depth** | Reference |

**Content:**
- YAML structure
- Duration and bandwidth format parsing
- Default value handling

### 4.2 forwarding section

| Field | Value |
|-------|-------|
| **ID** | 4.2 |
| **Purpose** | Document listener and flow management configuration |
| **Source artifacts** | internal/config/config.go:ForwardingConfig |
| **Dependencies** | 4.1 |
| **Audience** | Operators |
| **Depth** | Reference |

**Content:**
- listeners: bind_addr, bind_port, protocol, per-listener shaping
- limits: max_tcp_connections, max_udp_mappings
- idle_timeout: tcp, udp

### 4.3 upstreams section

| Field | Value |
|-------|-------|
| **ID** | 4.3 |
| **Purpose** | Document upstream definition and tuning |
| **Source artifacts** | internal/config/config.go:UpstreamConfig |
| **Dependencies** | 4.1 |
| **Audience** | Operators |
| **Depth** | Reference |

**Content:**
- tag, destination (host, port)
- measurement (host, port)
- priority, bias
- per-upstream shaping

### 4.4 dns section

| Field | Value |
|-------|-------|
| **ID** | 4.4 |
| **Purpose** | Document DNS resolution configuration |
| **Source artifacts** | internal/config/config.go:DNSConfig |
| **Dependencies** | 4.1 |
| **Audience** | Operators |
| **Depth** | Reference |

**Content:**
- servers list
- strategy (ipv4_only, prefer_ipv6)

### 4.5 reachability section

| Field | Value |
|-------|-------|
| **ID** | 4.5 |
| **Purpose** | Document ICMP reachability probe settings |
| **Source artifacts** | internal/config/config.go:ReachabilityConfig |
| **Dependencies** | 4.1 |
| **Audience** | Operators |
| **Depth** | Reference |

**Content:**
- probe_interval, window_size
- startup_delay

### 4.6 measurement section

| Field | Value |
|-------|-------|
| **ID** | 4.6 |
| **Purpose** | Document bwprobe measurement integration settings |
| **Source artifacts** | internal/config/config.go:MeasurementConfig |
| **Dependencies** | 4.1 |
| **Audience** | Operators |
| **Depth** | Reference |

**Content:**
- startup_delay, stale_threshold, fallback_to_icmp_on_stale
- schedule: interval (min/max), upstream_gap, headroom
- fast_start: enabled, timeout, warmup_duration
- protocols: tcp/udp test parameters

### 4.7 scoring section

| Field | Value |
|-------|-------|
| **ID** | 4.7 |
| **Purpose** | Document upstream quality scoring configuration |
| **Source artifacts** | internal/config/config.go:ScoringConfig |
| **Dependencies** | 4.1, 6.1 |
| **Audience** | Operators |
| **Depth** | Reference |

**Content:**
- smoothing: alpha (EMA)
- reference: per-protocol target values
- weights: per-metric and protocol blend weights
- utilization_penalty: window, threshold, multiplier, exponent
- bias_transform: kappa

### 4.8 switching section

| Field | Value |
|-------|-------|
| **ID** | 4.8 |
| **Purpose** | Document upstream switching behavior |
| **Source artifacts** | internal/config/config.go:SwitchingConfig |
| **Dependencies** | 4.1, 6.1 |
| **Audience** | Operators |
| **Depth** | Reference |

**Content:**
- auto: confirm_duration, score_delta_threshold, min_hold_time
- failover: loss_rate_threshold, retransmit_rate_threshold
- close_flows_on_failover

### 4.9 control section

| Field | Value |
|-------|-------|
| **ID** | 4.9 |
| **Purpose** | Document control plane configuration |
| **Source artifacts** | internal/config/config.go:ControlConfig |
| **Dependencies** | 4.1 |
| **Audience** | Operators |
| **Depth** | Reference |

**Content:**
- bind_addr, bind_port
- auth_token
- webui: enabled
- metrics: enabled

### 4.10 shaping section

| Field | Value |
|-------|-------|
| **ID** | 4.10 |
| **Purpose** | Document Linux tc traffic shaping |
| **Source artifacts** | internal/config/config.go:ShapingConfig |
| **Dependencies** | 4.1 |
| **Audience** | Operators |
| **Depth** | Reference |

**Content:**
- enabled
- interface, ifb_device
- aggregate_limit

---

## 5. API reference

### 5.1 bwprobe public API (bwprobe/pkg)

#### 5.1.1 Overview

| Field | Value |
|-------|-------|
| **ID** | 5.1.1 |
| **Purpose** | Introduce the bwprobe Go library for embedding |
| **Source artifacts** | bwprobe/pkg/doc.go |
| **Dependencies** | 3.2.1 |
| **Audience** | Developers |
| **Depth** | Overview |

**Content:**
- Import path
- Package purpose
- Relationship to CLI

#### 5.1.2 Types

| Field | Value |
|-------|-------|
| **ID** | 5.1.2 |
| **Purpose** | Document exported types |
| **Source artifacts** | bwprobe/pkg/config.go, bwprobe/pkg/results.go |
| **Dependencies** | 5.1.1 |
| **Audience** | Developers |
| **Depth** | Reference |

**Content:**
- Config struct and fields
- Result, SampleResult, IntervalStats types
- Error types

#### 5.1.3 Functions

| Field | Value |
|-------|-------|
| **ID** | 5.1.3 |
| **Purpose** | Document exported functions |
| **Source artifacts** | bwprobe/pkg/probe.go, bwprobe/pkg/rtt.go, bwprobe/pkg/sampler.go |
| **Dependencies** | 5.1.2 |
| **Audience** | Developers |
| **Depth** | Reference |

**Content:**
- Run() - execute a bandwidth test
- MeasureRTT() - measure round-trip time
- Sampler interface and NewSampler()
- Error handling patterns

#### 5.1.4 Examples

| Field | Value |
|-------|-------|
| **ID** | 5.1.4 |
| **Purpose** | Provide usage examples |
| **Source artifacts** | bwprobe/pkg/ (inline examples) |
| **Dependencies** | 5.1.3 |
| **Audience** | Developers |
| **Depth** | Reference |

**Content:**
- Basic measurement example
- Custom configuration
- Streaming results with Sampler

### 5.2 Control plane API

#### 5.2.1 Overview

| Field | Value |
|-------|-------|
| **ID** | 5.2.1 |
| **Purpose** | Introduce the fbforward HTTP/RPC control API |
| **Source artifacts** | internal/control/control.go |
| **Dependencies** | 3.1.3 |
| **Audience** | Operators, developers |
| **Depth** | Overview |

**Content:**
- Endpoint base URL
- Authentication (Bearer token)
- Content types

#### 5.2.2 RPC methods

| Field | Value |
|-------|-------|
| **ID** | 5.2.2 |
| **Purpose** | Document JSON-RPC methods |
| **Source artifacts** | internal/control/control.go |
| **Dependencies** | 5.2.1 |
| **Audience** | Operators, developers |
| **Depth** | Reference |

**Content:**
- GetStatus - retrieve current status
- ListUpstreams - list upstream states
- SetUpstream - manual upstream selection
- Restart - trigger config reload
- GetMeasurementConfig - retrieve measurement settings
- GetRuntimeConfig - retrieve runtime configuration
- GetScheduleStatus - retrieve measurement schedule status
- GetQueueStatus - retrieve measurement queue details
- RunMeasurement - trigger manual measurement

#### 5.2.3 WebSocket status stream

| Field | Value |
|-------|-------|
| **ID** | 5.2.3 |
| **Purpose** | Document real-time status subscription |
| **Source artifacts** | internal/control/control.go |
| **Dependencies** | 5.2.1 |
| **Audience** | Developers |
| **Depth** | Reference |

**Content:**
- WebSocket endpoint
- Authentication via subprotocol
- Message format
- Reconnection patterns

#### 5.2.4 Prometheus metrics

| Field | Value |
|-------|-------|
| **ID** | 5.2.4 |
| **Purpose** | Document exposed metrics |
| **Source artifacts** | internal/metrics/metrics.go |
| **Dependencies** | 5.2.1 |
| **Audience** | Operators |
| **Depth** | Reference |

**Content:**
- Metric names and labels
- Counter vs gauge vs histogram
- Scrape configuration

---

## 6. Algorithm specifications

### 6.1 Upstream selection algorithm

#### 6.1.1 Overview

| Field | Value |
|-------|-------|
| **ID** | 6.1.1 |
| **Purpose** | Introduce the upstream selection and scoring system |
| **Source artifacts** | docs/archive/2025-01-26-legacy/algorithm.md, internal/upstream/upstream.go |
| **Dependencies** | 1.2 |
| **Audience** | Operators, developers |
| **Depth** | Overview |

**Content:**
- Flow pinning model
- Primary upstream concept
- Score-based selection

#### 6.1.2 Formal description

| Field | Value |
|-------|-------|
| **ID** | 6.1.2 |
| **Purpose** | Provide mathematical specification of scoring |
| **Source artifacts** | docs/archive/2025-01-26-legacy/algorithm.md, internal/upstream/upstream.go |
| **Dependencies** | 6.1.1 |
| **Audience** | Developers |
| **Depth** | Deep-dive |

**Content:**
- Sub-score formulas (bandwidth, RTT, jitter, loss/retransmit)
- Base quality score calculation
- Utilization penalty
- Bias transformation
- Final score composition

#### 6.1.3 Parameters

| Field | Value |
|-------|-------|
| **ID** | 6.1.3 |
| **Purpose** | Document all algorithm parameters and their effects |
| **Source artifacts** | docs/archive/2025-01-26-legacy/algorithm.md, internal/config/config.go |
| **Dependencies** | 6.1.2 |
| **Audience** | Operators |
| **Depth** | Reference |

**Content:**
- Reference values table
- Weight configuration
- EMA smoothing factor
- Utilization penalty parameters

#### 6.1.4 Edge cases

| Field | Value |
|-------|-------|
| **ID** | 6.1.4 |
| **Purpose** | Document special conditions and failure modes |
| **Source artifacts** | internal/upstream/upstream.go |
| **Dependencies** | 6.1.2 |
| **Audience** | Operators, developers |
| **Depth** | Reference |

**Content:**
- Fast-start mode behavior
- Stale measurement handling
- Unusable upstream recovery
- Fast failover triggers

### 6.2 Bandwidth measurement algorithm (bwprobe)

#### 6.2.1 Overview

| Field | Value |
|-------|-------|
| **ID** | 6.2.1 |
| **Purpose** | Introduce the bwprobe measurement methodology |
| **Source artifacts** | docs/archive/2025-01-26-legacy/bwprobe/algorithm.md |
| **Dependencies** | 3.2.1 |
| **Audience** | Developers |
| **Depth** | Overview |

**Content:**
- Goals (accuracy, efficiency)
- Two-channel design rationale
- Sample-based testing model

#### 6.2.2 Formal description

| Field | Value |
|-------|-------|
| **ID** | 6.2.2 |
| **Purpose** | Provide detailed measurement algorithm specification |
| **Source artifacts** | docs/archive/2025-01-26-legacy/bwprobe/algorithm.md, bwprobe/internal/engine/samples.go |
| **Dependencies** | 6.2.1 |
| **Audience** | Developers |
| **Depth** | Deep-dive |

**Content:**
- Throughput calculation (trimmed mean, percentiles, sustained peak)
- Interval aggregation (100ms buckets)
- Server-side timing rationale

#### 6.2.3 Parameters

| Field | Value |
|-------|-------|
| **ID** | 6.2.3 |
| **Purpose** | Document bwprobe algorithm parameters |
| **Source artifacts** | bwprobe/pkg/config.go |
| **Dependencies** | 6.2.2 |
| **Audience** | Operators, developers |
| **Depth** | Reference |

**Content:**
- Target bandwidth
- Sample size and count
- Chunk size
- Timeout values

#### 6.2.4 Edge cases

| Field | Value |
|-------|-------|
| **ID** | 6.2.4 |
| **Purpose** | Document measurement edge cases |
| **Source artifacts** | bwprobe/internal/engine/ |
| **Dependencies** | 6.2.2 |
| **Audience** | Developers |
| **Depth** | Reference |

**Content:**
- Timeout handling
- Partial sample handling
- Loss and retransmit measurement
- Reverse mode differences

### 6.3 bwprobe RPC protocol

#### 6.3.1 Overview

| Field | Value |
|-------|-------|
| **ID** | 6.3.1 |
| **Purpose** | Introduce the bwprobe control protocol |
| **Source artifacts** | docs/archive/2025-01-26-legacy/bwprobe/rpc-protocol.md |
| **Dependencies** | 6.2.1 |
| **Audience** | Developers |
| **Depth** | Overview |

**Content:**
- Transport and framing
- JSON-RPC 2.0 envelope
- Protocol negotiation

#### 6.3.2 Formal description

| Field | Value |
|-------|-------|
| **ID** | 6.3.2 |
| **Purpose** | Specify protocol messages and sequences |
| **Source artifacts** | bwprobe/internal/rpc/protocol.go |
| **Dependencies** | 6.3.1 |
| **Audience** | Developers |
| **Depth** | Deep-dive |

**Content:**
- Session lifecycle (hello, heartbeat, goodbye)
- Sample methods (start, stop, report)
- Error codes

#### 6.3.3 Parameters

| Field | Value |
|-------|-------|
| **ID** | 6.3.3 |
| **Purpose** | Document protocol parameters |
| **Source artifacts** | bwprobe/internal/rpc/ |
| **Dependencies** | 6.3.2 |
| **Audience** | Developers |
| **Depth** | Reference |

**Content:**
- Message size limits
- Heartbeat intervals
- Timeout values

#### 6.3.4 Edge cases

| Field | Value |
|-------|-------|
| **ID** | 6.3.4 |
| **Purpose** | Document protocol error handling |
| **Source artifacts** | bwprobe/internal/rpc/ |
| **Dependencies** | 6.3.2 |
| **Audience** | Developers |
| **Depth** | Reference |

**Content:**
- Legacy protocol fallback
- Connection recovery
- Session expiry

---

## 7. Developer guide

### 7.1 Architecture deep dive

| Field | Value |
|-------|-------|
| **ID** | 7.1 |
| **Purpose** | Provide detailed architecture documentation for contributors |
| **Source artifacts** | internal/*, CLAUDE.md |
| **Dependencies** | 1.2 |
| **Audience** | Developers |
| **Depth** | Deep-dive |

**Content:**
- Package dependency graph
- Concurrency model (goroutine spawn points, context propagation)
- State management patterns
- Error handling conventions

### 7.2 Extension points

| Field | Value |
|-------|-------|
| **ID** | 7.2 |
| **Purpose** | Guide developers on extending fbforward |
| **Source artifacts** | internal/ interface definitions |
| **Dependencies** | 7.1 |
| **Audience** | Developers |
| **Depth** | Reference |

**Content:**
- Adding new protocol forwarders
- Extending the scoring algorithm
- Adding new RPC methods
- Adding new metrics

### 7.3 Contributing

| Field | Value |
|-------|-------|
| **ID** | 7.3 |
| **Purpose** | Document contribution workflow |
| **Source artifacts** | Makefile, .gitignore |
| **Dependencies** | 2.2 |
| **Audience** | Developers |
| **Depth** | Reference |

**Content:**
- Development setup
- Testing requirements
- Code style guidelines
- Pull request process

---

## 8. Appendices

### 8.1 Glossary

| Field | Value |
|-------|-------|
| **ID** | 8.1 |
| **Purpose** | Define domain terminology |
| **Source artifacts** | All sections |
| **Dependencies** | None |
| **Audience** | All readers |
| **Depth** | Reference |

**Content:** See [glossary.md](glossary.md)

### 8.2 Diagram index

| Field | Value |
|-------|-------|
| **ID** | 8.2 |
| **Purpose** | Catalog all architectural diagrams |
| **Source artifacts** | All sections |
| **Dependencies** | None |
| **Audience** | All readers |
| **Depth** | Reference |

**Content:** See [diagrams.md](diagrams.md)

---

## Cross-reference matrix

| Configuration option | Code location | User guide section |
|---------------------|---------------|-------------------|
| forwarding.listeners | internal/config/config.go:ForwardingConfig | 4.2, 3.1.2 |
| upstreams | internal/config/config.go:UpstreamConfig | 4.3, 3.1.2 |
| measurement.* | internal/config/config.go:MeasurementConfig | 4.6, 3.1.2 |
| scoring.* | internal/config/config.go:ScoringConfig | 4.7, 6.1.3 |
| switching.* | internal/config/config.go:SwitchingConfig | 4.8, 6.1.4 |
| control.* | internal/config/config.go:ControlConfig | 4.9, 5.2.1 |
| shaping.* | internal/config/config.go:ShapingConfig | 4.10, 3.1.2 |

| CLI tool | Primary guide | API reference |
|----------|--------------|---------------|
| fbforward | 3.1 | 5.2 |
| bwprobe | 3.2 | 5.1 |
| fbmeasure | 3.3 | - |

| Algorithm | Specification | Configuration |
|-----------|--------------|---------------|
| Upstream selection | 6.1 | 4.7, 4.8 |
| Bandwidth measurement | 6.2 | 4.6.protocols |
| RPC protocol | 6.3 | - |
