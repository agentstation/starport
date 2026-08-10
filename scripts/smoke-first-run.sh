#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temporary_root="${TMPDIR:-/tmp}"
temporary_root="${temporary_root%/}"
smoke_root="$(mktemp -d "$temporary_root/starport-first-run.XXXXXX")"
smoke_root="$(cd "$smoke_root" && pwd)"
server_pid=""

cleanup() {
	if [[ -n "$server_pid" ]] && kill -0 "$server_pid" 2>/dev/null; then
		kill -INT "$server_pid"
		wait "$server_pid" || true
	fi
	case "$smoke_root" in
		"$temporary_root"/starport-first-run.*) rm -rf "$smoke_root" ;;
	esac
}
trap cleanup EXIT INT TERM

for required_tool in curl go jq; do
	if ! command -v "$required_tool" >/dev/null 2>&1; then
		printf 'first-run smoke requires %s\n' "$required_tool" >&2
		exit 1
	fi
done

binary="$smoke_root/starport"
config_directory="$smoke_root/config"
server_log="$smoke_root/server.log"
setup_log="$smoke_root/setup.log"
server_port="${STARPORT_SMOKE_PORT:-18080}"

cd "$repository_root"
go build -trimpath -o "$binary" ./cmd/starport

export STARPORT_CONFIG_DIR="$config_directory"
export STARPORT_PROVIDERS_OPENAI_API_KEY="first-run-provider-key"
export STARPORT_SERVER_PORT="$server_port"

if ! initialization="$("$binary" init --provider openai --name smoke-admin --json 2>"$setup_log")"; then
	printf 'first-run initialization failed\n' >&2
	sed -n '1,120p' "$setup_log" >&2
	exit 1
fi
gateway_key="$(jq -er '.api_key | select(length > 0)' <<<"$initialization")"
test "$(jq -r .identity_name <<<"$initialization")" = smoke-admin
test "$(jq -r .config_file <<<"$initialization")" = "$config_directory/config.env"

if ! "$binary" config validate >>"$setup_log" 2>&1; then
	printf 'first-run configuration validation failed\n' >&2
	sed -n '1,120p' "$setup_log" >&2
	exit 1
fi
if ! "$binary" doctor --probe >>"$setup_log" 2>&1; then
	printf 'first-run diagnosis failed\n' >&2
	sed -n '1,120p' "$setup_log" >&2
	exit 1
fi

"$binary" serve >"$server_log" 2>&1 &
server_pid=$!

ready=false
for _ in {1..60}; do
	if curl --fail --silent "http://127.0.0.1:$server_port/health/ready" >/dev/null; then
		ready=true
		break
	fi
	if ! kill -0 "$server_pid" 2>/dev/null; then
		break
	fi
	sleep 0.25
done

if [[ "$ready" != true ]]; then
	printf 'first-run server did not become ready\n' >&2
	sed -n '1,120p' "$server_log" >&2
	exit 1
fi

curl --fail --silent \
	-H "Authorization: Bearer $gateway_key" \
	"http://127.0.0.1:$server_port/api/v1/models" |
	jq -e '.data | type == "array"' >/dev/null

printf 'PASS isolated init, validation, diagnosis, readiness, and authenticated model discovery\n'
