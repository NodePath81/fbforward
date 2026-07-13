#!/bin/sh
set -eu

# Download GeoIP databases outside fbforward, validate them, atomically replace
# the configured files, then ask the running process to reopen local readers.
: "${FBFORWARD_CONTROL_URL:=http://127.0.0.1:8080}"
: "${FBFORWARD_CONTROL_TOKEN:?set FBFORWARD_CONTROL_TOKEN}"
: "${ASN_URL:?set ASN_URL}"
: "${ASN_PATH:?set ASN_PATH}"

tmp=$(mktemp "${ASN_PATH}.tmp.XXXXXX")
trap 'rm -f "$tmp"' EXIT INT TERM
curl --fail --location --silent --show-error "$ASN_URL" -o "$tmp"
test -s "$tmp"
if command -v mmdblookup >/dev/null 2>&1; then
	mmdblookup --file "$tmp" --ip 1.1.1.1 >/dev/null
fi
install -m 0644 "$tmp" "$ASN_PATH"

curl --fail --silent --show-error \
	-H "Authorization: Bearer ${FBFORWARD_CONTROL_TOKEN}" \
	-H 'Content-Type: application/json' \
	-d '{"method":"ReloadGeoIP","params":null}' \
	"${FBFORWARD_CONTROL_URL}/rpc" >/dev/null
