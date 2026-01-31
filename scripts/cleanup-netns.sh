#!/usr/bin/env bash
set -euo pipefail

# Kill lingering test harness namespace shells and fbforward-test processes.
pids=$(pgrep -f "fbforward-testharness|unshare -Urn --kill-child=SIGTERM bash" || true)
if [[ -n "${pids}" ]]; then
  echo "Killing lingering processes: ${pids}"
  kill ${pids} 2>/dev/null || true
fi

# Delete stray test veths if present.
for dev in $(ip -o link show | awk -F': ' '{print $2}' | grep -E '^test0|^veth-fbfwd|^veth-us-'); do
  ip link delete "${dev}" 2>/dev/null || true
done
