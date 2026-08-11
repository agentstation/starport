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
	if [[ -n "$smoke_root" && ${smoke_root##*/} == starport-first-run.* ]]; then
		rm -rf -- "$smoke_root"
	fi
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
development_config_directory="$smoke_root/development-config"
development_log="$smoke_root/development.log"
server_log="$smoke_root/server.log"
setup_log="$smoke_root/setup.log"
server_port="${STARPORT_SMOKE_PORT:-18080}"

if [[ ! $server_port =~ ^[0-9]+$ ]] || ((server_port < 1 || server_port > 65535)); then
	printf 'STARPORT_SMOKE_PORT must be an integer from 1 through 65535\n' >&2
	exit 1
fi

cd "$repository_root"
go build -trimpath -o "$binary" ./cmd/starport

development_environment=(
	env -i
	"PATH=${PATH:-/usr/bin:/bin}"
	"STARPORT_CONFIG_DIR=$development_config_directory"
	"STARPORT_SERVER_PORT=$server_port"
)

"${development_environment[@]}" "$binary" dev >"$development_log" 2>&1 &
server_pid=$!

development_ready=false
for _ in {1..60}; do
	if curl --connect-timeout 1 --max-time 2 --fail --silent \
			"http://127.0.0.1:$server_port/health/ready" >/dev/null; then
		development_ready=true
		break
	fi
	if ! kill -0 "$server_pid" 2>/dev/null; then
		break
	fi
	sleep 0.25
done

if [[ "$development_ready" != true ]]; then
	printf 'development gateway did not become ready\n' >&2
	sed -n '1,120p' "$development_log" >&2
	exit 1
fi
development_key="$(sed -n 's/^Gateway API key (shown once): //p' "$development_log" | head -n 1)"
if [[ -z "$development_key" ]] || [[ "$(grep -cF -- "$development_key" "$development_log")" -ne 1 ]]; then
	printf 'development gateway did not print one gateway API key\n' >&2
	exit 1
fi
development_auth_status="$(curl --connect-timeout 2 --max-time 10 --silent \
	--output /dev/null --write-out '%{http_code}' \
	-H "Authorization: Bearer $development_key" \
	"http://127.0.0.1:$server_port/api/v1/admin/info")"
if [[ "$development_auth_status" != 200 ]]; then
	printf 'development authentication returned HTTP %s\n' "$development_auth_status" >&2
	exit 1
fi
if [[ -e "$development_config_directory" ]]; then
	printf 'development gateway created persistent configuration\n' >&2
	exit 1
fi
kill -INT "$server_pid"
wait "$server_pid"
server_pid=""

starport_environment=(
	env -i
	"PATH=${PATH:-/usr/bin:/bin}"
	"STARPORT_CONFIG_DIR=$config_directory"
	"OPENAI_API_KEY=first-run-provider-key"
	"STARPORT_SERVER_PORT=$server_port"
)

if ! initialization="$("${starport_environment[@]}" "$binary" init --name smoke-admin --json 2>"$setup_log")"; then
	printf 'first-run initialization failed\n' >&2
	sed -n '1,120p' "$setup_log" >&2
	exit 1
fi
gateway_key="$(jq -er '.api_key | select(length > 0)' <<<"$initialization")"
test "$(jq -r .identity_name <<<"$initialization")" = smoke-admin
test "$(jq -r .config_file <<<"$initialization")" = "$config_directory/config.env"
if grep -q '^OPENAI_API_KEY=' "$config_directory/config.env"; then
	printf 'first-run initialization persisted a provider credential\n' >&2
	exit 1
fi

if ! "${starport_environment[@]}" "$binary" config validate >>"$setup_log" 2>&1; then
	printf 'first-run configuration validation failed\n' >&2
	sed -n '1,120p' "$setup_log" >&2
	exit 1
fi
if ! "${starport_environment[@]}" "$binary" doctor --probe >>"$setup_log" 2>&1; then
	printf 'first-run diagnosis failed\n' >&2
	sed -n '1,120p' "$setup_log" >&2
	exit 1
fi

"${starport_environment[@]}" "$binary" serve >"$server_log" 2>&1 &
server_pid=$!

ready=false
for _ in {1..60}; do
	if curl --connect-timeout 1 --max-time 2 --fail --silent \
			"http://127.0.0.1:$server_port/health/ready" >/dev/null; then
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

curl --connect-timeout 2 --max-time 10 --fail --silent \
	-H "Authorization: Bearer $gateway_key" \
	"http://127.0.0.1:$server_port/api/v1/models" |
	jq -e '.data | type == "array"' >/dev/null

printf 'PASS ephemeral dev, isolated init, validation, diagnosis, readiness, and authenticated model discovery\n'
