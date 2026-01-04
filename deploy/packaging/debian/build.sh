#!/usr/bin/env sh
set -eu

SERVICE_NAME="fbforward"
ROOT_DIR="$(pwd)"
ARCH="$(dpkg --print-architecture 2>/dev/null || echo amd64)"
VERSION="${VERSION:-0.1.0}"
MAINTAINER="${MAINTAINER:-fbforward maintainer <root@localhost>}"

BIN_SRC="${ROOT_DIR}/fbforward"
CONFIG_SRC="${ROOT_DIR}/config.example.yaml"
SERVICE_SRC="${ROOT_DIR}/deploy/systemd/${SERVICE_NAME}.service"

BUILD_DIR="${ROOT_DIR}/deploy/packaging/debian/build"
PKG_DIR="${BUILD_DIR}/${SERVICE_NAME}_${VERSION}_${ARCH}"
OUTPUT_DIR="${OUTPUT_DIR:-${ROOT_DIR}/deploy/packaging/debian}"
OUTPUT_DEB="${OUTPUT_DIR}/${SERVICE_NAME}_${VERSION}_${ARCH}.deb"

rm -rf "$PKG_DIR"
mkdir -p "$PKG_DIR/DEBIAN" \
  "$PKG_DIR/usr/local/bin" \
  "$PKG_DIR/etc/fbforward" \
  "$PKG_DIR/etc/systemd/system"

if [ ! -f "$BIN_SRC" ]; then
  echo "Binary not found at $BIN_SRC; building..." >&2
  go build -o "$BIN_SRC" .
fi

if [ ! -f "$SERVICE_SRC" ]; then
  echo "Service file not found at $SERVICE_SRC" >&2
  exit 1
fi

install -m 0755 "$BIN_SRC" "$PKG_DIR/usr/local/bin/fbforward"
install -m 0644 "$SERVICE_SRC" "$PKG_DIR/etc/systemd/system/${SERVICE_NAME}.service"

if [ -f "$CONFIG_SRC" ]; then
  install -m 0640 "$CONFIG_SRC" "$PKG_DIR/etc/fbforward/config.yaml"
fi

cat <<CONTROL > "$PKG_DIR/DEBIAN/control"
Package: ${SERVICE_NAME}
Version: ${VERSION}
Section: net
Priority: optional
Architecture: ${ARCH}
Maintainer: ${MAINTAINER}
Depends: systemd
Description: fbforward userspace NAT-style TCP/UDP forwarder
CONTROL

install -m 0755 "${ROOT_DIR}/deploy/packaging/debian/postinst" "$PKG_DIR/DEBIAN/postinst"
install -m 0755 "${ROOT_DIR}/deploy/packaging/debian/prerm" "$PKG_DIR/DEBIAN/prerm"
install -m 0755 "${ROOT_DIR}/deploy/packaging/debian/postrm" "$PKG_DIR/DEBIAN/postrm"

mkdir -p "$BUILD_DIR" "$OUTPUT_DIR"

dpkg-deb --build "$PKG_DIR" "$OUTPUT_DEB"

echo "Built $OUTPUT_DEB"
