# Testing guide

This guide covers fbforward's test infrastructure: unit tests for algorithms and scoring, and an integration test harness that validates switching behavior using rootless network namespaces.

---

## 1. Overview

fbforward has two categories of tests:

- **Unit tests** (`*_test.go` files) cover bwprobe measurement algorithms, upstream scoring logic, configuration validation, control-plane RPC, GeoIP management, IP-log store/pipeline, firewall rule evaluation, forwarding, runtime lifecycle, and metrics. They run on any platform with Go installed (some tests are Linux-only).
- **Integration tests** use a rootless network-namespace harness to run real fbforward instances against simulated upstreams with controlled link conditions. They require Linux with unprivileged user namespace support.

Integration scenarios are harness-driven and are not executed by `go test ./...`.

For the separate manual coordination lab, see [coordlab.md](coordlab.md).

**Quick start:**

```bash
# All unit tests
go test ./...

# Unit tests (specific packages)
go test ./bwprobe/internal/... -v
go test ./internal/upstream ./internal/config ./internal/control ./internal/geoip ./internal/iplog/... ./internal/firewall ./internal/forwarding ./internal/app ./internal/metrics -v

# Integration tests (Linux only)
./scripts/setup-test-env.sh                    # preflight + build
./scripts/run-scenario.sh                      # quick sanity (score-ordering)
./scripts/run-scenario.sh -s score-ordering -s confirmation -s hold-time -s fast-failover -s anti-flapping -s stability

# Coordlab Phase 5 manual smoke
.venv/bin/python scripts/coordlab/coordlab.py up --skip-build --workdir /tmp/coordlab-phase5
.venv/bin/python scripts/coordlab/coordlab.py web --workdir /tmp/coordlab-phase5
# Open http://127.0.0.1:18800
.venv/bin/python scripts/coordlab/coordlab.py shaping-set --workdir /tmp/coordlab-phase5 --target upstream-1 --delay-ms 200
.venv/bin/python scripts/coordlab/coordlab.py shaping-clear-all --workdir /tmp/coordlab-phase5
.venv/bin/python scripts/coordlab/coordlab.py down --workdir /tmp/coordlab-phase5
```

---

## 2. Unit tests

### 2.1 bwprobe algorithm tests

| Test file | What it covers |
|-----------|---------------|
| `bwprobe/internal/engine/samples_test.go` | `trimmedMean` (empty/edge/10% trim), `percentile` (p0/p50/p80/p90/p100, single-element), `peakRollingWindow` (rate calculation, insufficient data) |
| `bwprobe/internal/metrics/rtt_sampler_test.go` | `RTTSampler.addSample` sequence -- verifies Mean, Min, Max, StdDev, and empty-sampler zero values |
| `bwprobe/internal/metrics/udp_test.go` | `Receiver.Add` with sequential/gapped/duplicate sequence numbers; `Reset` zeroing; loss counting |
| `bwprobe/internal/metrics/tcp_test.go` | `parseTCPInfo` with mock `unix.TCPInfo`: primary fields (`Data_segs_out`, `Total_retrans`), fallback tiers (byte-based estimation), all-zero input. **Linux-only** (`//go:build linux`) |
| `bwprobe/internal/network/ratelimit_test.go` | `Limiter.Wait` timing at 10 MB/s (generous tolerance for CI), nil/zero-rate pass-through |

Run:

```bash
go test ./bwprobe/internal/... -v
```

### 2.2 Scoring logic tests

File: `internal/upstream/upstream_test.go`

| Test | What it verifies |
|------|-----------------|
| `TestComputeFullScoreOrdering` | Better metrics (higher bandwidth, lower RTT/jitter/loss) produce a higher overall score |
| `TestComputeFullScoreStalenessPenalty` | Stale TCP data (1h old vs. fresh) reduces the score below the fresh-data score |
| `TestComputeFullScoreBiasTransform` | +0.5 bias increases score above neutral; -0.5 decreases below neutral |
| `TestApplyEMA` | First call returns raw value and sets init flag; subsequent calls blend at alpha=0.2 |

All scoring tests use `config.DefaultScoringConfig()` to obtain a valid configuration without a config file.

Run:

```bash
go test ./internal/upstream -v
```

### 2.3 Configuration validation tests

File: `internal/config/config_test.go`

| Test | What it verifies |
|------|-----------------|
| `TestCoordinationConfig*` (3) | Coordination block is optional; requires fields together when present; accepts complete block |
| `TestGeoIPConfig*` (4) | Requires at least one complete URL+path pair when enabled; rejects incomplete pairs; rejects path without URL |
| `TestIPLogConfig*` (3) | Requires `db_path` when enabled; applies default queue sizes; rejects invalid tuning (zero/negative) |
| `TestFirewall*` (3) | Rules require exactly one matcher; country codes normalized to uppercase; invalid default/action rejected |

### 2.4 Control-plane RPC tests

File: `internal/control/control_test.go`

| Test | What it verifies |
|------|-----------------|
| `TestRPCRejectsMissingBearerToken` | Auth enforcement |
| `TestRPCRejectsWrongHTTPMethod` | Only POST accepted |
| `TestGetGeoIPStatus*` (4) | Unavailable without manager; returns configured status; accepts omitted/null params; handles unconfigured/single-DB |
| `TestRefreshGeoIP*` (3) | Unavailable without manager; no-op without configured DBs; returns per-DB results |
| `TestGetIPLogStatus*` (3) | Unavailable without store; returns stats; handles empty/stat-failure |
| `TestQueryIPLog*` (6) | Unavailable without store; rejects CIDR without time bound; returns paginated results; rejects malformed paging; sort validation; combined filters |
| `TestRuntimeConfigIncludesIPLogTuning` | `GetRuntimeConfig` includes all config sections |

### 2.5 GeoIP manager tests

File: `internal/geoip/manager_test.go`

| Test | What it verifies |
|------|-----------------|
| `TestLookupSupportsPartialAvailability` | Lookup succeeds when only one DB is loaded |
| `TestStatusReports*` (3) | Status payload includes file metadata and reader availability |
| `TestRefresh*` (4) | Failure preserves existing reader; success swaps atomically; partial success; no-op without configured DBs |
| `TestRefreshNow*` (5) | Returns per-DB results; reports missing files; serializes concurrent calls |
| `TestLookupDuringRefreshSeesConsistentValues` | Concurrent reads see consistent ASN+country |
| `TestCloseIsIdempotent` | Close can be called multiple times |
| `TestLoadLocalReadersWithSingleConfiguredDB` | Startup with one DB type configured |

### 2.6 IP-log store tests

File: `internal/iplog/store_test.go`

| Test | What it verifies |
|------|-----------------|
| `TestStoreInsertAndQuery` | Basic insert and query round-trip |
| `TestStoreStats` / `TestStoreStatsEmpty` | Stats reporting (row count, DB size, oldest entry) |
| `TestQueryRequiresTimeBoundForCIDR` | CIDR filter requires time bounds to prevent full-table scans |
| `TestPruneRemovesOldRows` | Retention pruning deletes expired rows |
| `TestStoreReopenPreservesSchemaAndData` | Schema survives close/reopen |
| `TestQuery*` (5) | Combined filters, pagination, invalid bounds/CIDR, sort params, stable ordering |
| `TestFileSizeGrowsAcrossBatches` | DB size metric accuracy |

### 2.7 IP-log pipeline tests

File: `internal/iplog/pipeline_test.go`

| Test | What it verifies |
|------|-----------------|
| `TestPipelineFlushesOnShutdown` | Pending events flushed on graceful stop |
| `TestPipelineDropsWhenGeoQueueIsFull` | Events dropped when enrichment queue overflows |
| `TestPipelineFlushesOnBatchSize` | Batch flushes at configured size |
| `TestPipelineFlushesOnTimer` | Partial batch flushes after flush interval |
| `TestPipelineWritesPartialGeoIPData` | Records with partial enrichment are written |
| `TestPipelineWriteQueueOverflowIncrementsDropMetric` | Write-queue overflow counted |
| `TestPipelineShutdownWithEmptyQueues` | Clean shutdown with no pending data |

### 2.8 Firewall engine tests

File: `internal/firewall/engine_test.go`

| Test | What it verifies |
|------|-----------------|
| `TestCIDRDenyRule` | CIDR matching works for deny rules |
| `TestFirstMatchWins` | Rule evaluation stops at first match |
| `TestASNRuleSkippedWhenDBUnavailable` | ASN rules fail open without GeoIP ASN DB |
| `TestCountryRuleMatches` | Country matching with GeoIP data |
| `TestCountryRuleSkippedWhenDBUnavailable` | Country rules fail open without GeoIP country DB |
| `TestIPv6CIDRRuleMatches` | IPv6 CIDR matching |
| `TestDenyMetricUsesRuleLabels` | Deny metrics include correct `rule_type` and `rule_value` labels |

### 2.9 Other package tests

| File | What it covers |
|------|---------------|
| `internal/forwarding/forwarding_test.go` | TCP/UDP forwarding logic and flow pinning |
| `internal/app/runtime_test.go` | Runtime lifecycle, component wiring |
| `internal/metrics/metrics_test.go` | Prometheus metric rendering, IP-log/firewall metric output |

### 2.10 Frontend verification

The web UI (including the IP Log page) can be verified by building the frontend:

```bash
cd web && npm run build
```

A successful build confirms that TypeScript sources compile without errors.

### 2.11 Writing new unit tests

- Use table-driven tests with `t.Run` subtests.
- For scoring tests, use `config.DefaultScoringConfig()` to get valid defaults.
- For Linux-only tests (TCP metrics), add `//go:build linux` at the top.
- Rate-limiter timing tests use generous tolerance (5x expected) to avoid CI flakiness.

---

## 3. Integration tests

### 3.1 Architecture

Integration tests use nested Linux user namespaces to create isolated network environments without root privileges.

```
Host (no privileges)
  └── ns0 (userns via unshare -Urn --kill-child=SIGTERM)
       ├── Gains CAP_NET_ADMIN inside its own userns
       ├── Owns all veth pairs and tc qdisc rules
       └── Children: fbfwd, us-a, us-b, ...
            └── Each child is a nested userns with one veth peer moved in
```

Key properties:

- **Star topology**: ns0 at the center, each upstream/fbforward namespace connected to ns0 via a veth pair.
- **nsenter execution**: All commands inside namespaces run via `nsenter -t <shell_pid> -n <command>`.
- **tc shaping**: Applied on ns0-side veth interfaces. Two-level qdisc: `tbf` root (rate limit) + `netem` child (latency/loss).
- **Symmetric only**: Same shaping on both directions of each link.

### 3.2 Prerequisites

| Requirement | Minimum | How to verify |
|------------|---------|--------------|
| Kernel | 3.8+ with `CONFIG_USER_NS=y` | `unshare -Urn echo OK` |
| Unprivileged userns | Enabled | `sysctl kernel.unprivileged_userns_clone` must be 1 |
| iproute2 | v4.9+ | `ip link set <dev> netns <path>` support |
| Go | 1.23+ | `go version` |
| iperf3 | Any recent version | `iperf3 --version` |

The preflight script (`scripts/setup-test-env.sh`) validates all of these. If any check fails, it aborts with a diagnostic message.

### 3.3 Preflight validation

`scripts/setup-test-env.sh` performs a full nested-namespace test before building:

1. Checks required tools: `ip`, `tc`, `iperf3`, `unshare`, `go`.
2. Creates ns0 via `unshare -Urn`.
3. Spawns a child userns inside ns0.
4. Creates a veth pair, moves one peer into the child namespace.
5. Applies tc shaping (`tbf` at 100mbit).
6. Assigns IPs and pings across namespaces.
7. Cleans up via `trap cleanup EXIT` (kills child, deletes test interfaces).
8. If any step fails: aborts with `exit 1`. No fallback.
9. On success: runs `make build` and builds the test harness CLI.

### 3.4 Process lifecycle

- **Launch**: Via `nsenter -t <pid> -n <binary> <args>`. Stdout/stderr captured to log files in the work directory.
- **Termination**: SIGTERM first, wait 5 seconds, then SIGKILL.
- **Emergency cleanup**: If the harness crashes, run `scripts/cleanup-netns.sh` to kill orphaned `unshare` shells and remove stray veth interfaces.

### 3.5 Running integration tests

```bash
# First-time setup (preflight + build)
./scripts/setup-test-env.sh

# Validate scenario YAML without executing
build/bin/fbforward-testharness validate test/scenarios/score-ordering.yaml

# Run a single scenario
build/bin/fbforward-testharness run test/scenarios/score-ordering.yaml

# Run all 6 scenarios sequentially
./scripts/run-scenario.sh

# Emergency cleanup after crashes
./scripts/cleanup-netns.sh
```

The harness CLI (`cmd/fbforward-testharness/main.go`) supports two subcommands:
- `validate <scenario.yaml>` -- parse and validate without running
- `run <scenario.yaml>` -- execute full lifecycle: Setup -> Start -> Run -> Verify -> ExportArtifacts -> Cleanup

---

## 3.8 coordlab manual test framework

coordlab is a separate Python-based manual lab for `fbcoord` and coordinated `fbforward` nodes. It does not reuse the scenario runner and is intended for interactive testing, browser-based inspection, and manual upstream degradation.

Use coordlab when you need:

- a local `fbcoord` plus two coordinated `fbforward` nodes
- host-accessible UIs and RPC endpoints
- live delay/loss shaping on upstream links
- the Flask dashboard for status, coordination, shaping, and log viewing

See [coordlab.md](coordlab.md) for the full architecture, command set, file layout, and dashboard/API behavior.

### 3.6 Metrics collection

The harness scrapes fbforward's Prometheus endpoint (`/metrics`) using Bearer token authentication. It parses these metrics from Prometheus text format:

| Metric | Type | Used for |
|--------|------|----------|
| `fbforward_active_upstream{upstream="..."}` | gauge | Identifying the current primary upstream |
| `fbforward_upstream_score_tcp{upstream="..."}` | gauge | Per-upstream TCP score |
| `fbforward_upstream_score_udp{upstream="..."}` | gauge | Per-upstream UDP score |
| `fbforward_upstream_score{upstream="..."}` | gauge | Per-upstream overall score |
| `fbforward_memory_alloc_bytes` | gauge | Heap memory tracking (stability assertions) |
| `fbforward_goroutines` | gauge | Goroutine count tracking (stability assertions) |

Each scrape produces a `MetricsSample` with: `Timestamp`, `PrimaryTag`, `UpstreamScores`, `SwitchCount`, `MemoryBytes`, `Goroutines`.

### 3.7 Auth token handling

The harness generates an fbforward config with a known token (`test-harness-token-12345` in the base templates). It writes the config to a temp file, passes the path to fbforward at launch, then uses the same token for authenticated metrics scraping.

---

## 4. Scenario YAML format

Scenarios define integration test cases as YAML files in `test/scenarios/`. Each scenario references a base config template from `test/testdata/` and specifies overrides, link shaping, and a timeline of actions and assertions.

### 4.1 Top-level fields

```yaml
name: string                # Scenario identifier
interval_seconds: int       # Measurement interval (must match base config)
convergence_cycles: int     # Number of stable cycles before declaring convergence
overrides: map              # Deep-merged into base config
shaping: map                # Initial tc rules per upstream
timeline: []                # Ordered sequence of actions and assertions
```

### 4.2 Override semantics

The `overrides` map is deep-merged into the base config:
- **Maps**: Deep merge (keys merged recursively).
- **Arrays and primitives**: Full replacement.
- **Syntax**: Nested YAML only, no dot-path notation.

Example:

```yaml
overrides:
  switching:
    auto:
      confirm_duration: "10s"    # Merges into switching.auto, other keys preserved
```

### 4.3 Shaping rules

```yaml
shaping:
  us-a: {bandwidth: "100mbit", latency: "20ms", loss: "0%"}
  us-b: {bandwidth: "50mbit", latency: "40ms", loss: "0%"}
```

Each rule specifies `bandwidth` (tbf rate), `latency` (netem delay), and `loss` (netem loss percentage). Rules are symmetric -- applied identically in both directions.

### 4.4 Timeline actions

| Action | Description | Extra fields |
|--------|-------------|-------------|
| `wait_convergence` | Block until primary is stable for `convergence_cycles` consecutive measurement intervals | -- |
| `degrade_upstream` | Apply new shaping to an upstream's link | `upstream`, `new_shaping` |
| `restore_upstream` | Reset shaping to initial values | `upstream`, `new_shaping` |
| `inject_loss` | Apply packet loss to an upstream | `upstream`, `new_shaping` |
| `perturb_bandwidth` | Adjust bandwidth on an upstream | `upstream`, `new_shaping` |

### 4.5 Timeline assertions

| Type | Description |
|------|-------------|
| `primary_is` | Assert the active upstream matches a given tag |
| `stability_ok` | Assert process health, memory growth < 50MB, goroutine growth < 20, API latency < 100ms |
| `switch_count_lte` | Assert total switch count is within a threshold |

### 4.6 Timing tolerance

Assertions use timing tolerance to account for measurement intervals:

- **Positive assertions** ("is now X"): deadline = stated time x 1.1 + one measurement interval (5s).
  - Example: "switch by t=15s" -> deadline = 15 x 1.1 + 5 = 21.5s.
- **Negative assertions** ("still X"): window = stated time +/- 10%.
  - Example: "still us-a at t=7s" -> window = 6.3s to 7.7s.

### 4.7 Base config templates

| Template | Upstreams | Key settings |
|----------|-----------|-------------|
| `test/testdata/fbforward-2up.yaml` | us-a (`10.200.2.1`), us-b (`10.200.3.1`) | 5s interval, auth token, TCP+UDP, 5s startup delay, 30s stale threshold, metrics enabled, webui disabled |
| `test/testdata/fbforward-3up.yaml` | us-a, us-b, us-c (`10.200.4.1`) | Same as 2-up plus a third upstream |

---

## 5. Scenario reference

| Scenario | Base | Key override | Duration | What it validates |
|----------|------|-------------|----------|------------------|
| `score-ordering` | 2-up | (none) | ~20s | Higher-quality upstream (100Mbps/20ms) selected as primary over lower-quality (50Mbps/40ms) within 3 convergence cycles |
| `confirmation` | 2-up | `confirm_duration: 10s` | ~25s | Degrade us-a at t=0; still us-a at t=7s (window not elapsed); switched to us-b by t=15s |
| `hold-time` | 2-up | `min_hold_time: 20s` | ~40s | Degrade us-a, restore at t=5s; still us-b at t=15s (hold time active); back to us-a by t=30s |
| `fast-failover` | 2-up | (none) | ~20s | Inject 25% loss on us-a; switch to us-b within 2 cycles (10s) |
| `anti-flapping` | 2-up | `score_delta_threshold: 5.0` | ~2min | Perturb bandwidth +/-5Mbps every 15s; total switch count <= 2 at end |
| `stability` | 3-up | (none) | ~10min | 3 upstreams, long run; assert process alive, memory delta < 50MB, goroutine delta < 20, API latency < 100ms |

---

## 6. Scripts

### 6.1 `scripts/setup-test-env.sh`

Preflight validation and build script. Checks tools (`ip`, `tc`, `iperf3`, `unshare`, `go`), runs a full nested-userns preflight with cleanup trap, then builds fbforward and the test harness binary.

### 6.2 `scripts/run-scenario.sh`

Runs selected scenarios sequentially via `fbforward-testharness run`. If no flags are provided, runs all default scenarios. Supports `-s <name>` for scenario names and `-f/--file <path>` for explicit scenario files. Reports per-scenario pass/fail and exits non-zero if any scenario failed.

### 6.3 `scripts/cleanup-netns.sh`

Emergency cleanup for orphaned processes. Kills processes matching `fbforward-testharness` or `unshare -Urn --kill-child` patterns. Deletes stray veth interfaces (`test0`, `veth-fbfwd-*`, `veth-us-*`). Only needed when the harness crashes before normal cleanup.

---

## 7. Known limitations

- **Harness is scaffold-level**: Start, Run, Verify, and ExportArtifacts methods are stubbed. Full end-to-end integration runs require further implementation of topology setup (child namespaces, veth/IP assignment), config generation, process health checks, timeline execution, and detailed assertion evaluation.
- **iperf3 integration**: Server/client launch functions are stubs.
- **Linux only**: Integration tests cannot run on macOS, BSD, or Windows.
- **Docker**: Containers may need `--privileged` or `--cap-add=SYS_ADMIN` for nested user namespaces.
- **Go build cache**: The default Go build cache may emit permission warnings. Use `GOCACHE=/tmp/gocache` if needed.

---

## 8. Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `unshare: unrecognized option '--kill-child'` | util-linux too old | Upgrade to util-linux 2.32+ |
| `Operation not permitted` on `unshare` | Unprivileged user namespaces disabled | `sudo sysctl kernel.unprivileged_userns_clone=1` |
| tc commands fail | Missing `CONFIG_NET_SCH_TBF` or `CONFIG_NET_SCH_NETEM` kernel modules | Load modules or recompile kernel |
| `fbforward-testharness: command not found` | Harness not built | Run `./scripts/setup-test-env.sh` |
| Orphaned namespace processes after crash | Harness did not clean up | Run `./scripts/cleanup-netns.sh` |
| Metrics scraping 401/connection refused | Auth token mismatch or fbforward not running | Check config auth token; check process logs in work directory |
| Go cache permission errors | Default GOCACHE path not writable | Export `GOCACHE=/tmp/gocache` before building |
