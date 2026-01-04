#!/usr/bin/env sh
set -eu

SERVICE_NAME="fbforward"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
USER_NAME="fbforward"
GROUP_NAME="fbforward"
BIN_DEST="/usr/local/bin/fbforward"
CONFIG_DIR="/etc/fbforward"
CONFIG_DEST="${CONFIG_DIR}/config.yaml"

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

if ! getent group "$GROUP_NAME" >/dev/null 2>&1; then
  groupadd --system "$GROUP_NAME"
fi

if ! id -u "$USER_NAME" >/dev/null 2>&1; then
  useradd --system --gid "$GROUP_NAME" --home /nonexistent --shell /usr/sbin/nologin "$USER_NAME" || \
    useradd --system --gid "$GROUP_NAME" --home /nonexistent --shell /sbin/nologin "$USER_NAME"
fi

install -m 0755 "$BIN_SRC" "$BIN_DEST"

mkdir -p "$CONFIG_DIR"
if [ -n "$CONFIG_SRC" ]; then
  install -m 0640 "$CONFIG_SRC" "$CONFIG_DEST"
  chown root:"$GROUP_NAME" "$CONFIG_DEST"
else
  if [ ! -f "$CONFIG_DEST" ]; then
    echo "No config provided; copy config.example.yaml to $CONFIG_DEST and update it." >&2
  fi
fi

install -m 0644 "$SERVICE_SRC" "$SERVICE_FILE"

systemctl daemon-reload
systemctl enable --now "$SERVICE_NAME"

echo "Installed and started ${SERVICE_NAME}."
