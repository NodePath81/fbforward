#!/bin/sh
set -eu

# Download GeoIP databases outside fbforward, validate them, atomically replace
# the configured files, then ask the running process to reopen local readers.
: "${FBFORWARD_CONTROL_URL:=http://127.0.0.1:8080}"
: "${FBFORWARD_CONTROL_TOKEN:?set FBFORWARD_CONTROL_TOKEN}"
ASN_URL=${ASN_URL:-}
ASN_PATH=${ASN_PATH:-}
COUNTRY_URL=${COUNTRY_URL:-}
COUNTRY_PATH=${COUNTRY_PATH:-}
MMDBLOOKUP_BIN=${MMDBLOOKUP_BIN:-mmdblookup}

if [ -z "$ASN_URL" ] && [ -z "$COUNTRY_URL" ]; then
	echo "set ASN_URL/ASN_PATH or COUNTRY_URL/COUNTRY_PATH" >&2
	exit 2
fi
if [ -n "$ASN_URL" ] && [ -z "$ASN_PATH" ]; then
	echo "ASN_PATH is required when ASN_URL is set" >&2
	exit 2
fi
if [ -z "$ASN_URL" ] && [ -n "$ASN_PATH" ]; then
	echo "ASN_URL is required when ASN_PATH is set" >&2
	exit 2
fi
if [ -n "$COUNTRY_URL" ] && [ -z "$COUNTRY_PATH" ]; then
	echo "COUNTRY_PATH is required when COUNTRY_URL is set" >&2
	exit 2
fi
if [ -z "$COUNTRY_URL" ] && [ -n "$COUNTRY_PATH" ]; then
	echo "COUNTRY_URL is required when COUNTRY_PATH is set" >&2
	exit 2
fi
if ! command -v "$MMDBLOOKUP_BIN" >/dev/null 2>&1; then
	echo "mmdblookup is required for GeoIP database validation" >&2
	exit 2
fi

flush_path() {
	if sync -f "$1" 2>/dev/null; then
		return
	fi
	sync
}

update_database() {
	url=$1
	path=$2
	kind=$3
	: "${url:?database URL is required}"
	: "${path:?database path is required}"
	tmp=$(mktemp "${path}.tmp.XXXXXX")
	trap 'rm -f "$tmp"' EXIT INT TERM
	curl --fail --location --silent --show-error "$url" -o "$tmp"
	test -s "$tmp"
	case "$kind" in
	ASN) "$MMDBLOOKUP_BIN" --file "$tmp" --ip 1.1.1.1 autonomous_system_number >/dev/null ;;
	COUNTRY) "$MMDBLOOKUP_BIN" --file "$tmp" --ip 1.1.1.1 country iso_code >/dev/null ;;
	*) echo "unknown database type: $kind" >&2; return 2 ;;
	esac
	chmod 0644 "$tmp"
	flush_path "$tmp"
	mv -f "$tmp" "$path"
	flush_path "$(dirname "$path")"
	trap - EXIT INT TERM
}

if [ -n "$ASN_URL" ]; then
	update_database "$ASN_URL" "$ASN_PATH" ASN
fi
if [ -n "$COUNTRY_URL" ]; then
	update_database "$COUNTRY_URL" "$COUNTRY_PATH" COUNTRY
fi

curl --fail --silent --show-error \
	-H "Authorization: Bearer ${FBFORWARD_CONTROL_TOKEN}" \
	-H 'Content-Type: application/json' \
	-d '{"method":"ReloadGeoIP","params":null}' \
	"${FBFORWARD_CONTROL_URL}/rpc" >/dev/null
