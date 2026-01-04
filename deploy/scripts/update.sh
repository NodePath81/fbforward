#!/usr/bin/env sh
set -eu

SERVICE_NAME="fbforward"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
BIN_DEST="/usr/local/bin/fbforward"
CONFIG_DIR="/etc/fbforward"
CONFIG_DEST="${CONFIG_DIR}/config.yaml"
GROUP_NAME="fbforward"

ROOT_DIR="$(pwd)"
BIN_SRC="${1:-${ROOT_DIR}/fbforward}"
CONFIG_SRC="${2:-""}"
SERVICE_SRC="${ROOT_DIR}/deploy/systemd/${SERVICE_NAME}.service"

if [ "$(id -u)" -ne 0 ]; then
  echo "This script must be run as root." >&2
  exit 1
fi

if [ ! -f "$BIN_SRC" ]; then
  echo "Binary not found at $BIN_SRC" >&2
  echo "Run from the project root or pass the binary path as arg 1." >&2
  exit 1
fi

if [ ! -f "$SERVICE_SRC" ]; then
  echo "Service file not found at $SERVICE_SRC" >&2
  echo "Run from the project root or set SERVICE_SRC in this script." >&2
  exit 1
fi

install -m 0755 "$BIN_SRC" "$BIN_DEST"
install -m 0644 "$SERVICE_SRC" "$SERVICE_FILE"

if [ -n "$CONFIG_SRC" ]; then
  mkdir -p "$CONFIG_DIR"
  install -m 0640 "$CONFIG_SRC" "$CONFIG_DEST"
  if getent group "$GROUP_NAME" >/dev/null 2>&1; then
    chown root:"$GROUP_NAME" "$CONFIG_DEST"
  fi
fi

systemctl daemon-reload
systemctl restart "$SERVICE_NAME"

echo "Updated ${SERVICE_NAME}."
