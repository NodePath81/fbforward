#!/usr/bin/env sh
set -eu

SERVICE_NAME="fbforward"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
BIN_DEST="/usr/local/bin/fbforward"
CONFIG_DIR="/etc/fbforward"
USER_NAME="fbforward"
GROUP_NAME="fbforward"

PURGE="${1:-""}"

if [ "$(id -u)" -ne 0 ]; then
  exec sudo "$0" "$@"
fi

systemctl disable --now "$SERVICE_NAME" 2>/dev/null || true

if [ -f "$SERVICE_FILE" ]; then
  rm -f "$SERVICE_FILE"
  systemctl daemon-reload
fi

if [ -f "$BIN_DEST" ]; then
  rm -f "$BIN_DEST"
fi

if [ "$PURGE" = "--purge" ]; then
  rm -rf "$CONFIG_DIR"
  if id -u "$USER_NAME" >/dev/null 2>&1; then
    userdel "$USER_NAME" || true
  fi
  if getent group "$GROUP_NAME" >/dev/null 2>&1; then
    groupdel "$GROUP_NAME" || true
  fi
  echo "Removed ${SERVICE_NAME} and purged config/user."\
  
else
  echo "Removed ${SERVICE_NAME} service and binary."
  echo "Config kept at ${CONFIG_DIR}. Use --purge to remove it and the system user."\
  
fi
