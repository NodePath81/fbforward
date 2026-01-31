#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${ROOT}/build/bin/fbforward-testharness"
LOG_DIR="/tmp/fbforward-testharness"

if [[ ! -x "${BIN}" ]]; then
  echo "Harness binary not found at ${BIN}; building..." >&2
  (cd "${ROOT}" && go build -o build/bin/fbforward-testharness ./cmd/fbforward-testharness)
fi

export PATH="${ROOT}/build/bin:${PATH}"

echo "run-scenario: root=${ROOT}"
echo "run-scenario: bin=${BIN}"
echo "run-scenario: pwd=$(pwd)"
echo "run-scenario: PATH=${PATH}"

echo "run-scenario: which.fbforward-testharness=$(command -v fbforward-testharness || true)"
echo "run-scenario: which.fbforward=$(command -v fbforward || true)"
echo "run-scenario: which.fbmeasure=$(command -v fbmeasure || true)"
echo "run-scenario: which.iperf3=$(command -v iperf3 || true)"

echo "run-scenario: ls.bin=$(ls -l "${BIN}")"
echo "run-scenario: go.version=$(go version 2>/dev/null || true)"

ALL_SCENARIOS=(
  "${ROOT}/test/scenarios/score-ordering.yaml"
  "${ROOT}/test/scenarios/confirmation.yaml"
  "${ROOT}/test/scenarios/hold-time.yaml"
  "${ROOT}/test/scenarios/fast-failover.yaml"
  "${ROOT}/test/scenarios/anti-flapping.yaml"
  "${ROOT}/test/scenarios/stability.yaml"
)

usage() {
  cat <<USAGE
Usage: $(basename "$0") [-s <name>]... [-f|--file <path>]...

Options:
  -s <name>            Run scenario by name (repeatable).
  -f, --file <path>    Run scenario by file path (repeatable).
  -h, --help           Show this help.

Behavior:
  - If both -s and -f are provided, runs the union in the given order.
  - If neither is provided, runs a quick sanity check (default: score-ordering).
USAGE
}

selected=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    -s)
      if [[ $# -lt 2 ]]; then
        echo "Missing scenario name for -s" >&2
        usage
        exit 2
      fi
      selected+=("name:$2")
      shift 2
      ;;
    -f|--file)
      if [[ $# -lt 2 ]]; then
        echo "Missing scenario path for $1" >&2
        usage
        exit 2
      fi
      selected+=("file:$2")
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown arg: $1" >&2
      usage
      exit 2
      ;;
  esac
 done

resolved=()
if [[ ${#selected[@]} -eq 0 ]]; then
  resolved=("${ROOT}/test/scenarios/score-ordering.yaml")
else
  for item in "${selected[@]}"; do
    kind="${item%%:*}"
    val="${item#*:}"
    if [[ "${kind}" == "file" ]]; then
      if [[ -f "${val}" ]]; then
        resolved+=("${val}")
      elif [[ -f "${ROOT}/test/scenarios/${val}" ]]; then
        resolved+=("${ROOT}/test/scenarios/${val}")
      else
        echo "Scenario file not found: ${val}" >&2
        exit 2
      fi
    elif [[ "${kind}" == "name" ]]; then
      name="${val}"
      found=false
      for s in "${ALL_SCENARIOS[@]}"; do
        if [[ "$(basename "${s}")" == "${name}.yaml" ]]; then
          resolved+=("${s}")
          found=true
          break
        fi
      done
      if [[ "${found}" == false ]]; then
        echo "Scenario name not found: ${name}" >&2
        exit 2
      fi
    fi
  done
fi

mkdir -p "${LOG_DIR}"
RUN_LOG="${LOG_DIR}/quick-e2e.log"
: > "${RUN_LOG}"

printf "Selected scenarios (%d):\n" "${#resolved[@]}"
for s in "${resolved[@]}"; do
  echo "- ${s}"
 done

echo "Cleaning namespaces before run..."
"${ROOT}/scripts/cleanup-netns.sh" >/dev/null 2>&1 || true

fail=0
for s in "${resolved[@]}"; do
  echo "Running ${s}..."
  if "${BIN}" run "${s}" 2>&1 | tee -a "${RUN_LOG}"; then
    echo "PASS: ${s}"
  else
    echo "FAIL: ${s}"
    fail=1
  fi
 done

if [[ "${fail}" -eq 0 ]]; then
  echo "All scenarios completed"
else
  echo "Some scenarios failed"
  echo "--- harness log (tail) ---"
  tail -n 120 "${RUN_LOG}" || true
  if [[ -d "${LOG_DIR}/logs" ]]; then
    echo "--- fbforward log (tail) ---"
    tail -n 120 "${LOG_DIR}/logs/fbforward.log" 2>/dev/null || true
    echo "--- fbmeasure logs (tail) ---"
    for f in "${LOG_DIR}"/logs/fbmeasure-*.log; do
      [[ -e "$f" ]] || continue
      echo "==> $f <=="
      tail -n 120 "$f" || true
    done
  fi
fi

echo "Cleaning namespaces after run..."
"${ROOT}/scripts/cleanup-netns.sh" >/dev/null 2>&1 || true

exit "${fail}"
