#!/bin/sh
set -eu

# Download the two MMDB files used by fbforward from Loyalsoldier's release
# branch, verify the published checksums, atomically replace local files, and
# ask a running fbforward process to reopen them.

: "${LOYALSOLDIER_GEOIP_BASE_URL:=https://raw.githubusercontent.com/Loyalsoldier/geoip/release}"
: "${FBFORWARD_GEOIP_DIR:=/var/lib/fbforward}"
: "${FBFORWARD_CONTROL_URL:=http://127.0.0.1:8080}"
: "${FBFORWARD_RELOAD:=1}"

ASN_FILE=GeoLite2-ASN.mmdb
COUNTRY_FILE=Country-without-asn.mmdb

case "$FBFORWARD_RELOAD" in
  0|1) ;;
  *)
    echo "FBFORWARD_RELOAD must be 0 or 1" >&2
    exit 2
    ;;
esac

for command in curl sha256sum awk grep mktemp mv chmod mkdir rm; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "required command not found: $command" >&2
    exit 2
  fi
done

mkdir -p "$FBFORWARD_GEOIP_DIR"
work_dir=$(mktemp -d "${FBFORWARD_GEOIP_DIR}/.geoip-update.XXXXXX")
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM

download() {
  file=$1
  curl \
    --fail \
    --location \
    --silent \
    --show-error \
    --proto '=https' \
    --tlsv1.2 \
    --connect-timeout 15 \
    --max-time 300 \
    --retry 3 \
    --retry-delay 2 \
    --retry-connrefused \
    "${LOYALSOLDIER_GEOIP_BASE_URL}/${file}" \
    --output "${work_dir}/${file}"
  curl \
    --fail \
    --location \
    --silent \
    --show-error \
    --proto '=https' \
    --tlsv1.2 \
    --connect-timeout 15 \
    --max-time 60 \
    --retry 3 \
    --retry-delay 2 \
    --retry-connrefused \
    "${LOYALSOLDIER_GEOIP_BASE_URL}/${file}.sha256sum" \
    --output "${work_dir}/${file}.sha256sum"
}

verify() {
  file=$1
  expected=$(awk 'NR == 1 { print $1; exit }' "${work_dir}/${file}.sha256sum")
  case "$expected" in
    ''|*[!0-9a-fA-F]*)
      echo "invalid published checksum for ${file}" >&2
      exit 1
      ;;
  esac
  if [ "${#expected}" -ne 64 ]; then
    echo "invalid published checksum length for ${file}" >&2
    exit 1
  fi
  actual=$(sha256sum "${work_dir}/${file}" | awk '{ print $1 }')
  if [ "$actual" != "$expected" ]; then
    echo "checksum mismatch for ${file}" >&2
    exit 1
  fi
  if [ ! -s "${work_dir}/${file}" ]; then
    echo "downloaded database is empty: ${file}" >&2
    exit 1
  fi
}

is_changed() {
  file=$1
  destination="${FBFORWARD_GEOIP_DIR}/${file}"
  if [ ! -f "$destination" ]; then
    return 0
  fi
  current=$(sha256sum "$destination" | awk '{ print $1 }')
  incoming=$(sha256sum "${work_dir}/${file}" | awk '{ print $1 }')
  [ "$current" != "$incoming" ]
}

install_database() {
  file=$1
  chmod 0644 "${work_dir}/${file}"
  mv -f "${work_dir}/${file}" "${FBFORWARD_GEOIP_DIR}/${file}"
}

download "$ASN_FILE"
download "$COUNTRY_FILE"
verify "$ASN_FILE"
verify "$COUNTRY_FILE"

changed=0
if is_changed "$ASN_FILE"; then
  install_database "$ASN_FILE"
  changed=1
fi
if is_changed "$COUNTRY_FILE"; then
  install_database "$COUNTRY_FILE"
  changed=1
fi

if [ "$FBFORWARD_RELOAD" -eq 0 ]; then
  if [ "$changed" -eq 0 ]; then
    echo "GeoIP databases are already current; runtime reload disabled"
  else
    echo "GeoIP databases updated; runtime reload disabled"
  fi
  exit 0
fi

: "${FBFORWARD_CONTROL_TOKEN:?set FBFORWARD_CONTROL_TOKEN to reload fbforward}"
response_file="${work_dir}/reload-response.json"
auth_header="${work_dir}/control-auth-header"
case "$FBFORWARD_CONTROL_URL" in
  http://127.0.0.1:*|http://localhost:*|http://\[::1\]:*|https://*) ;;
  *)
    echo "refusing to send the control token over non-loopback HTTP" >&2
    exit 2
    ;;
esac
if printf '%s' "$FBFORWARD_CONTROL_TOKEN" | LC_ALL=C grep -q '[[:cntrl:]]'; then
  echo "FBFORWARD_CONTROL_TOKEN must not contain control characters" >&2
  exit 2
fi
umask 077
printf 'Authorization: Bearer %s\n' "$FBFORWARD_CONTROL_TOKEN" >"$auth_header"
umask 022
curl \
  --fail \
  --silent \
  --show-error \
  --connect-timeout 5 \
  --max-time 15 \
  --header "@${auth_header}" \
  --header 'Content-Type: application/json' \
  --data '{"method":"ReloadGeoIP","params":null}' \
  "${FBFORWARD_CONTROL_URL}/rpc" \
  --output "$response_file"

if ! grep -Eq '"ok"[[:space:]]*:[[:space:]]*true' "$response_file"; then
  echo "fbforward rejected ReloadGeoIP" >&2
  exit 1
fi

if [ "$changed" -eq 0 ]; then
  echo "GeoIP databases are current and runtime reload succeeded"
else
  echo "GeoIP databases updated and reloaded"
fi
