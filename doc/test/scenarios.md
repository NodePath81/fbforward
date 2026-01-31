# Integration test scenarios

This document describes the scenario YAML files in `test/scenarios/`. Each file defines one integration test case for the fbforward test harness.

---

## Scenarios

| Scenario | File | Base config | Duration | What it validates |
|----------|------|------------|----------|------------------|
| Score ordering | `score-ordering.yaml` | 2-up | ~20s | Higher-quality upstream selected as primary |
| Confirmation | `confirmation.yaml` | 2-up | ~25s | `confirm_duration` delays switching |
| Hold time | `hold-time.yaml` | 2-up | ~40s | `min_hold_time` prevents premature flip-back |
| Fast failover | `fast-failover.yaml` | 2-up | ~20s | High packet loss triggers fast switch |
| Anti-flapping | `anti-flapping.yaml` | 2-up | ~2min | `score_delta_threshold` suppresses oscillation |
| Stability | `stability.yaml` | 3-up | ~10min | Long-run health (memory, goroutines, API latency) |

Base configs: `test/testdata/fbforward-2up.yaml` (2 upstreams) and `test/testdata/fbforward-3up.yaml` (3 upstreams). Both use 5s measurement intervals, auth token `test-harness-token-12345`, TCP+UDP enabled.

---

## Scenario details

### score-ordering

Validates basic score-driven upstream selection. us-a has better link conditions (100Mbps/20ms) than us-b (50Mbps/40ms). Asserts us-a becomes primary after convergence (3 cycles).

```yaml
name: score-ordering
interval_seconds: 5
convergence_cycles: 3
shaping:
  us-a: {bandwidth: "100mbit", latency: "20ms", loss: "0%"}
  us-b: {bandwidth: "50mbit", latency: "40ms", loss: "0%"}
timeline:
  - t: "0s"
    action: wait_convergence
    assertions:
      - type: primary_is
        upstream: us-a
```

### confirmation

Validates that `confirm_duration` delays switching. After convergence on us-a, degrades us-a. At t=7s (before 10s confirmation window), us-a should still be primary. By t=15s, switch to us-b should have occurred.

```yaml
name: confirmation
overrides:
  switching:
    auto:
      confirm_duration: "10s"
shaping:
  us-a: {bandwidth: "100mbit", latency: "20ms", loss: "0%"}
  us-b: {bandwidth: "50mbit", latency: "40ms", loss: "0%"}
timeline:
  - t: "0s"
    action: wait_convergence
    assertions: [{type: primary_is, upstream: us-a}]
  - t: "0s"
    action: degrade_upstream
    upstream: us-a
    new_shaping: {bandwidth: "30mbit", latency: "60ms", loss: "0%"}
  - t: "7s"
    assertions: [{type: primary_is, upstream: us-a}]
  - t: "15s"
    assertions: [{type: primary_is, upstream: us-b}]
```

### hold-time

Validates that `min_hold_time` prevents flip-flopping. After convergence, degrades us-a, then restores it at t=5s. At t=15s, us-b should still be primary (hold time active). By t=30s, us-a should be primary again.

```yaml
name: hold-time
overrides:
  switching:
    auto:
      min_hold_time: "20s"
shaping:
  us-a: {bandwidth: "100mbit", latency: "20ms", loss: "0%"}
  us-b: {bandwidth: "50mbit", latency: "40ms", loss: "0%"}
timeline:
  - t: "0s"
    action: wait_convergence
  - t: "0s"
    action: degrade_upstream
    upstream: us-a
    new_shaping: {bandwidth: "30mbit", latency: "60ms", loss: "0%"}
  - t: "5s"
    action: restore_upstream
    upstream: us-a
    new_shaping: {bandwidth: "100mbit", latency: "20ms", loss: "0%"}
  - t: "15s"
    assertions: [{type: primary_is, upstream: us-b}]
  - t: "30s"
    assertions: [{type: primary_is, upstream: us-a}]
```

### fast-failover

Validates fast switching when packet loss is injected. After convergence on us-a, injects 25% loss. Asserts switch to us-b within 2 cycles (10s).

```yaml
name: fast-failover
shaping:
  us-a: {bandwidth: "100mbit", latency: "20ms", loss: "0%"}
  us-b: {bandwidth: "50mbit", latency: "40ms", loss: "0%"}
timeline:
  - t: "0s"
    action: wait_convergence
  - t: "0s"
    action: inject_loss
    upstream: us-a
    new_shaping: {bandwidth: "100mbit", latency: "20ms", loss: "25%"}
  - t: "10s"
    assertions: [{type: primary_is, upstream: us-b}]
```

### anti-flapping

Validates that `score_delta_threshold` suppresses oscillation between closely-matched upstreams. us-a (80Mbps) and us-b (75Mbps) are perturbed +/-5Mbps every 15s for 2 minutes. Asserts total switch count <= 2.

```yaml
name: anti-flapping
overrides:
  switching:
    auto:
      score_delta_threshold: 5.0
shaping:
  us-a: {bandwidth: "80mbit", latency: "25ms", loss: "0%"}
  us-b: {bandwidth: "75mbit", latency: "27ms", loss: "0%"}
timeline:
  - t: "0s"
    action: wait_convergence
  - t: "15s"
    action: perturb_bandwidth
    upstream: us-a
    new_shaping: {bandwidth: "85mbit", latency: "25ms", loss: "0%"}
  - t: "30s"
    action: perturb_bandwidth
    upstream: us-b
    new_shaping: {bandwidth: "72mbit", latency: "27ms", loss: "0%"}
  - t: "120s"
    assertions: [{type: switch_count_lte, reason: "anti-flapping"}]
```

### stability

Long-running test with 3 upstreams. Runs for 10 minutes and asserts process health: process alive, memory growth < 50MB, goroutine growth < 20, API latency < 100ms.

```yaml
name: stability
shaping:
  us-a: {bandwidth: "100mbit", latency: "20ms", loss: "0%"}
  us-b: {bandwidth: "80mbit", latency: "20ms", loss: "0%"}
  us-c: {bandwidth: "60mbit", latency: "20ms", loss: "0%"}
timeline:
  - t: "0s"
    action: wait_convergence
  - t: "600s"
    assertions: [{type: stability_ok, reason: "10-minute stability run"}]
```

---

## Running scenarios

```bash
# Setup and build (first time)
./scripts/setup-test-env.sh

# Validate YAML syntax
build/bin/fbforward-testharness validate test/scenarios/score-ordering.yaml

# Run a single scenario
build/bin/fbforward-testharness run test/scenarios/score-ordering.yaml

# Run all scenarios
./scripts/run-all-scenarios.sh
```

---

## Adding a new scenario

1. Copy an existing scenario YAML as a template.
2. Set `name`, `interval_seconds` (must match base config: 5), and `convergence_cycles`.
3. Define `overrides` for any config changes needed (deep-merged into base config).
4. Define `shaping` for initial link conditions per upstream.
5. Define `timeline` with actions (`wait_convergence`, `degrade_upstream`, etc.) and assertions (`primary_is`, `stability_ok`, `switch_count_lte`).
6. Add the file path to the `SCENARIOS` array in `scripts/run-all-scenarios.sh`.

See [testing-guide.md](testing-guide.md) for YAML format details and timing tolerance rules.
