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
Usage: $(basename "$0") [-s <name>]... [-f|--file <path>]... [-l <file>] [-q]

Options:
  -s <name>            Run scenario by name (repeatable).
  -f, --file <path>    Run scenario by file path (repeatable).
  -l <file>            Tee all logs to the given file.
  -q                   Quiet mode (no stdout). Still writes to -l if provided.
  -h, --help           Show this help.

Behavior:
  - If both -s and -f are provided, runs the union in the given order.
  - If neither is provided, runs a quick sanity check (default: score-ordering).
USAGE
}

selected=()
log_file=""
quiet=false
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
    -l)
      if [[ $# -lt 2 ]]; then
        echo "Missing file path for -l" >&2
        usage
        exit 2
      fi
      log_file="$2"
      shift 2
      ;;
    -q)
      quiet=true
      shift
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
RUN_LOG="${LOG_DIR}/run-scenario.log"
: > "${RUN_LOG}"

sink() {
  if [[ -n "${log_file}" ]]; then
    if [[ "${quiet}" == true ]]; then
      tee -a "${log_file}" >/dev/null
    else
      tee -a "${log_file}"
    fi
  else
    if [[ "${quiet}" == true ]]; then
      cat >/dev/null
    else
      cat
    fi
  fi
}

prefix_stream() {
  local prefix="$1"
  sed -u "s|^|[${prefix}] |" | sink
}

prefix_stream_file() {
  local prefix="$1"
  local file="$2"
  if [[ ! -f "${file}" ]]; then
    return
  fi
  tail -n 0 -F "${file}" | sed -u "s|^|[${prefix}] |" | sink
}

if [[ "${quiet}" != true ]]; then
  {
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
    printf "Selected scenarios (%d):\n" "${#resolved[@]}"
    for s in "${resolved[@]}"; do
      echo "- ${s}"
    done
  } | prefix_stream "meta"
else
  printf "Selected scenarios (%d):\n" "${#resolved[@]}" | prefix_stream "meta"
  for s in "${resolved[@]}"; do
    echo "- ${s}" | prefix_stream "meta"
  done
fi

echo "Cleaning namespaces before run..." | prefix_stream "meta"
"${ROOT}/scripts/cleanup-netns.sh" >/dev/null 2>&1 || true

fail=0
for s in "${resolved[@]}"; do
  scenario_name="$(basename "${s}" .yaml)"
  echo "Running ${s}..." | prefix_stream "runner/${scenario_name}"

  fbforward_log="${LOG_DIR}/logs/fbforward.log"
  fbmeasure_logs=("${LOG_DIR}"/logs/fbmeasure-*.log)
  iperf_client_log="${LOG_DIR}/logs/iperf3-client.log"
  iperf_server_logs=("${LOG_DIR}"/logs/iperf3-server-*.log)

  prefix_stream_file "fbforward/${scenario_name}" "${fbforward_log}" &
  tail_fbforward_pid=$!

  tail_fbmeasure_pids=()
  for f in "${fbmeasure_logs[@]}"; do
    [[ -e "${f}" ]] || continue
    prefix_stream_file "fbmeasure/${scenario_name}" "${f}" &
    tail_fbmeasure_pids+=("$!")
  done

  tail_iperf_pids=()
  if [[ -f "${iperf_client_log}" ]]; then
    prefix_stream_file "iperf3-client/${scenario_name}" "${iperf_client_log}" &
    tail_iperf_pids+=("$!")
  fi
  for f in "${iperf_server_logs[@]}"; do
    [[ -e "${f}" ]] || continue
    tag=$(basename "${f}" .log | sed 's/^iperf3-server-//')
    prefix_stream_file "iperf3-server-${tag}/${scenario_name}" "${f}" &
    tail_iperf_pids+=("$!")
  done

  if "${BIN}" run "${s}" 2>&1 | prefix_stream "harness/${scenario_name}"; then
    echo "PASS: ${s}" | prefix_stream "runner/${scenario_name}"
  else
    echo "FAIL: ${s}" | prefix_stream "runner/${scenario_name}"
    fail=1
  fi

  kill "${tail_fbforward_pid}" >/dev/null 2>&1 || true
  for pid in "${tail_fbmeasure_pids[@]}"; do
    kill "${pid}" >/dev/null 2>&1 || true
  done
  for pid in "${tail_iperf_pids[@]}"; do
    kill "${pid}" >/dev/null 2>&1 || true
  done
 done

if [[ "${fail}" -eq 0 ]]; then
  echo "All scenarios completed" | prefix_stream "meta"
else
  echo "Some scenarios failed" | prefix_stream "meta"
fi

echo "Cleaning namespaces after run..." | prefix_stream "meta"
"${ROOT}/scripts/cleanup-netns.sh" >/dev/null 2>&1 || true

exit "${fail}"
