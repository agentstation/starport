#!/bin/sh

set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
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

export STARPORT_SMOKE_BASE_URL="$base_url"
export STARPORT_SMOKE_API_KEY="$api_key"

python_status=0
if command -v python3 >/dev/null 2>&1; then
	python3 "$repository_root/scripts/smoke_openrouter_python.py" || python_status=$?
	if [ "$python_status" -ne 0 ] && [ "$python_status" -ne 3 ]; then
		exit "$python_status"
	fi
else
	printf '%s\n' 'UNVERIFIED Python OpenRouter SDK: python3 is not installed'
fi

typescript_status=0
if command -v node >/dev/null 2>&1; then
	node "$repository_root/scripts/smoke_openrouter_typescript.mjs" || typescript_status=$?
	if [ "$typescript_status" -ne 0 ] && [ "$typescript_status" -ne 3 ]; then
		exit "$typescript_status"
	fi
else
	printf '%s\n' 'UNVERIFIED TypeScript OpenRouter SDK: node is not installed'
fi

printf '%s\n' 'UNVERIFIED Go OpenRouter SDK: package is not part of this module'
