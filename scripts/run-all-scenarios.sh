#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${ROOT}/build/bin/fbforward-testharness"

if [[ ! -x "${BIN}" ]]; then
  echo "Harness binary not found at ${BIN}; run scripts/setup-test-env.sh first." >&2
  exit 1
fi

SCENARIOS=(
  "${ROOT}/test/scenarios/score-ordering.yaml"
  "${ROOT}/test/scenarios/confirmation.yaml"
  "${ROOT}/test/scenarios/hold-time.yaml"
  "${ROOT}/test/scenarios/fast-failover.yaml"
  "${ROOT}/test/scenarios/anti-flapping.yaml"
  "${ROOT}/test/scenarios/stability.yaml"
)

fail=0
for s in "${SCENARIOS[@]}"; do
  echo "Running ${s}..."
  if ! "${BIN}" run "${s}"; then
    echo "Scenario failed: ${s}"
    fail=1
  fi
done

if [[ "${fail}" -eq 0 ]]; then
  echo "All scenarios completed"
else
  echo "Some scenarios failed"
fi
exit "${fail}"
