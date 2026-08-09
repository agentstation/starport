#!/bin/sh

set -eu

# Never let a caller-wide Node.js TLS override weaken package installation or
# SDK requests. The local smoke server itself uses plain loopback HTTP.
unset NODE_TLS_REJECT_UNAUTHORIZED

repository_root=$(cd -- "$(dirname -- "$0")/.." && pwd)
temporary_directory=$(mktemp -d)
output_file="$temporary_directory/server.out"
error_file="$temporary_directory/server.err"
server_binary="$temporary_directory/sdk-smoke-server"
server_pid=""

cleanup() {
	if [ -n "$server_pid" ]; then
		kill "$server_pid" 2>/dev/null || true
		wait "$server_pid" 2>/dev/null || true
	fi
	rm -rf "$temporary_directory"
}
trap cleanup EXIT INT TERM

for required_tool in curl go node npm python3; do
	if ! command -v "$required_tool" >/dev/null 2>&1; then
		printf 'FAIL required SDK smoke tool is missing: %s\n' "$required_tool" >&2
		exit 1
	fi
done

(cd "$repository_root" && go build -o "$server_binary" ./scripts/sdk-smoke-server)
"$server_binary" >"$output_file" 2>"$error_file" &
server_pid=$!

base_url=""
attempt=0
while [ "$attempt" -lt 100 ]; do
	base_url=$(sed -n '/^http:\/\/127\.0\.0\.1:[0-9][0-9]*$/p' "$output_file" | sed -n '1p')
	if [ -n "$base_url" ]; then
		break
	fi
	if ! kill -0 "$server_pid" 2>/dev/null; then
		cat "$output_file"
		cat "$error_file" >&2
		exit 1
	fi
	attempt=$((attempt + 1))
	sleep 0.1
done

if [ -z "$base_url" ]; then
	printf '%s\n' 'FAIL smoke server did not start'
	cat "$output_file"
	cat "$error_file" >&2
	exit 1
fi

api_key="STARPORT_smoke_key"
api_base="$base_url/api/v1"

chat_response=$(curl --fail --silent --show-error \
	-H "Authorization: Bearer $api_key" \
	-H 'Content-Type: application/json' \
	-d '{"model":"openai/gpt-4.1","messages":[{"role":"user","content":"smoke"}]}' \
	"$api_base/chat/completions")
printf '%s' "$chat_response" | grep -q '"content":"starport smoke ok"'
printf '%s\n' 'PASS raw HTTP chat'

stream_response=$(curl --fail --silent --show-error --no-buffer \
	-H "Authorization: Bearer $api_key" \
	-H 'Content-Type: application/json' \
	-d '{"model":"openai/gpt-4.1","messages":[{"role":"user","content":"smoke"}],"stream":true,"stream_options":{"include_usage":true}}' \
	"$api_base/chat/completions")
printf '%s' "$stream_response" | grep -q 'data: \[DONE\]'
printf '%s\n' 'PASS raw HTTP stream'

models_response=$(curl --fail --silent --show-error \
	-H "Authorization: Bearer $api_key" \
	"$api_base/models")
printf '%s' "$models_response" | grep -q '"total_count":1'
printf '%s\n' 'PASS raw HTTP models'

embeddings_response=$(curl --fail --silent --show-error \
	-H "Authorization: Bearer $api_key" \
	-H 'Content-Type: application/json' \
	-d '{"model":"openai/text-embedding-3-small","input":"smoke"}' \
	"$api_base/embeddings")
printf '%s' "$embeddings_response" | grep -q '"embedding":\[0.1,0.2,0.3\]'
printf '%s\n' 'PASS raw HTTP embeddings'

export STARPORT_SMOKE_BASE_URL="$api_base"
export STARPORT_SMOKE_API_KEY="$api_key"

python_environment="$temporary_directory/python"
python3 -m venv "$python_environment"
"$python_environment/bin/python" -m pip install \
	--disable-pip-version-check \
	--quiet \
	'openrouter==1.1.38'
"$python_environment/bin/python" "$repository_root/scripts/smoke_openrouter_python.py"

typescript_environment="$temporary_directory/typescript"
mkdir -p "$typescript_environment"
npm install \
	--prefix "$typescript_environment" \
	--ignore-scripts \
	--no-audit \
	--no-fund \
	--silent \
	'@openrouter/sdk@1.2.18'
cp "$repository_root/scripts/smoke_openrouter_typescript.mjs" \
	"$typescript_environment/smoke_openrouter_typescript.mjs"
node "$typescript_environment/smoke_openrouter_typescript.mjs"

(cd "$repository_root/scripts/smoke_openrouter_go" && GOWORK=off go run .)
