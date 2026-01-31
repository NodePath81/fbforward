#!/usr/bin/env bash
set -euo pipefail

check_tool() {
  command -v "$1" >/dev/null 2>&1 || { echo "missing tool: $1" >&2; exit 1; }
}

check_tool ip
check_tool tc
check_tool iperf3
check_tool unshare
check_tool go
check_tool curl

# Preflight: validate nested userns + veth move + tc + connectivity
unshare -Urn bash -c '
  set -euo pipefail
  unshare -Urn bash -c "sleep 5" &
  CHILD_PID=$!
  sleep 0.2
  cleanup() {
    kill $CHILD_PID 2>/dev/null || true
    ip link delete test0 2>/dev/null || true
  }
  trap cleanup EXIT

  ip link add test0 type veth peer name test1
  ip link set test1 netns /proc/$CHILD_PID/ns/net
  tc qdisc add dev test0 root tbf rate 100mbit burst 32k latency 50ms
  ip link add ifb-test type ifb
  ip link set ifb-test up
  ip link del ifb-test
  ip addr add 192.168.99.1/24 dev test0
  ip link set test0 up
  nsenter -t $CHILD_PID -n ip addr add 192.168.99.2/24 dev test1
  nsenter -t $CHILD_PID -n ip link set test1 up
  nsenter -t $CHILD_PID -n ping -c 1 -W 1 192.168.99.1 >/dev/null
  echo "Preflight OK"
' || { echo "ABORT: rootless network preflight failed" >&2; exit 1; }

echo "Building test harness"
make build
go build -o build/bin/fbforward-testharness ./cmd/fbforward-testharness
