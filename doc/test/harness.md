# Integration test harness

Package `test/harness` provides the integration test harness for fbforward. It creates rootless network namespaces to run real fbforward instances against simulated upstreams with controlled link conditions.

---

## Architecture

The harness creates a star topology using nested Linux user namespaces. No root privileges are required.

```
Host (no privileges)
  └── ns0 (userns via unshare -Urn --kill-child=SIGTERM)
       ├── CAP_NET_ADMIN (within its own userns)
       ├── Owns all veth pairs and tc qdisc rules
       └── Children: fbfwd, us-a, us-b, ...
            └── Each child is a nested userns with one veth peer moved in
```

ns0 acts as the central router. Each child namespace has a single veth peer and communicates through ns0. Traffic shaping (tbf + netem) is applied on ns0-side interfaces.

All processes (fbforward, fbmeasure, iperf3) are launched via `nsenter -t <shell_pid> -n <binary> <args>`.

---

## Components

| File | Responsibility | Status |
|------|---------------|--------|
| `harness.go` | Orchestration lifecycle: NewHarness -> Setup -> Start -> Run -> Verify -> ExportArtifacts -> Cleanup | Setup creates ns0; Start/Run/Verify/ExportArtifacts are stubs |
| `scenario.go` | YAML parsing (`LoadScenario`), `Scenario`/`TimelineEvent`/`ShapingRule`/`TimelineAssertion` types | Complete |
| `topology.go` | `Namespace`/`VethPair`/`Topology` types, `LaunchNamespaceShell` (creates ns0 userns shell) | ns0 creation works; child namespace/veth/IP setup not implemented |
| `process.go` | `ProcessManager` with `Start` (nsenter launch + log capture), `Stop` (SIGTERM -> 5s -> SIGKILL), `StopAll` | Launch and termination work; health check polling not implemented |
| `shaping.go` | `ApplyShaping` -- runs tc qdisc del/add commands via nsenter in ns0 | Apply works; `UpdateShaping` (mid-test changes) not implemented |
| `metrics.go` | `MetricsCollector` with `CollectOnce` (scrape + parse Prometheus text), `parsePromLine` helper | Single-scrape works; periodic collection loop not implemented |
| `assertions.go` | `DetectConvergence` (N consecutive samples with same primary), `EvaluateAssertions` | Convergence detection works; assertion evaluation is a placeholder |
| `iperf3.go` | `Iperf3Result` type, `StartIperf3Server`, `RunIperf3Client` | Stubs only |

---

## Lifecycle

```
NewHarness(workDir, scenario)
  │
  ├── Setup()
  │     ├── LaunchNamespaceShell()   → creates ns0
  │     ├── (future) create child namespaces, veth pairs, assign IPs
  │     └── (future) ApplyShaping for initial link conditions
  │
  ├── Start()         → (stub) launch fbforward, fbmeasure, iperf3
  ├── Run()           → (stub) execute timeline actions
  ├── Verify()        → (stub) evaluate assertions against collected metrics
  ├── ExportArtifacts() → (stub) copy logs, metrics snapshots
  │
  └── Cleanup()
        ├── StopAll()              → SIGTERM/SIGKILL all tracked processes
        └── Topology.Cleanup()     → kill ns0 shell (propagates to children)
```

---

## Key types

### Namespace

```go
type Namespace struct {
    Name     string        // "ns0", "fbfwd", "us-a", etc.
    Index    int           // Numeric index for IP assignment
    IP       string        // Assigned IP address
    Gateway  string        // Gateway IP in ns0
    VethPair *VethPair     // Inner/Outer interface names
    ShellCmd *exec.Cmd     // The unshare shell process
    ShellPID int           // PID for nsenter operations
    ParentNS *Namespace    // ns0 for children, nil for ns0 itself
}
```

### Scenario

```go
type Scenario struct {
    Name              string
    IntervalSeconds   int
    ConvergenceCycles int
    Overrides         map[string]any            // Deep-merged into base config
    Shaping           map[string]ShapingRule     // Initial tc rules per upstream
    Timeline          []TimelineEvent           // Actions + assertions
}
```

### MetricsSample

```go
type MetricsSample struct {
    Timestamp      time.Time
    PrimaryTag     string                      // Active upstream tag
    UpstreamScores map[string]UpstreamScores   // Per-upstream TCP/UDP/Overall scores
    SwitchCount    int
    MemoryBytes    uint64                      // Heap allocation
    Goroutines     int
}
```

---

## Extending the harness

### Adding new timeline actions

1. Add the action string constant (e.g., `"my_action"`) to usage in scenario YAML.
2. Implement handling in `Harness.Run()` (currently stubbed in `harness.go`).
3. Document the action in [testing-guide.md](testing-guide.md) section 4.4.

### Adding new assertion types

1. Define the type string (e.g., `"my_assertion"`).
2. Implement evaluation logic in `EvaluateAssertions()` in `assertions.go`.
3. Document the assertion in [testing-guide.md](testing-guide.md) section 4.5.

### Adding new scraped metrics

1. Add a field to `MetricsSample` in `metrics.go`.
2. Add a `case` branch in `CollectOnce()` to parse the new Prometheus metric name.
3. Use the field in assertion evaluation as needed.

---

## CLI entry point

`cmd/fbforward-testharness/main.go` provides the harness CLI:

```
Usage: fbforward-testharness <run|validate> <scenario.yaml>
```

- `validate` -- loads and parses the scenario YAML, reports errors.
- `run` -- executes the full harness lifecycle (Setup through Cleanup). Work directory: `$TMPDIR/fbforward-testharness`.

---

## Prerequisites

See [testing-guide.md](testing-guide.md) section 3.2 for kernel and tool requirements.
