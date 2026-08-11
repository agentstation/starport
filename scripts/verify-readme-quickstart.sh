#!/usr/bin/env bash

set -euo pipefail

repository_root="${STARPORT_README_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
readme="$repository_root/README.md"

if [[ ! -f "$readme" ]]; then
	printf 'README is missing: %s\n' "$readme" >&2
	exit 1
fi

if rg -q 'STARPORT_BIN|init --provider' "$readme"; then
	printf 'README contains an obsolete binary variable or provider-specific init command\n' >&2
	exit 1
fi

quickstart="$(awk '
	/^## Quick start$/ { capture=1; next }
	capture && /^## / { exit }
	capture { print }
' "$readme")"
terminal_one="$(awk '
	/^### Terminal 1:/ { capture=1; next }
	capture && /^### / { exit }
	capture { print }
' "$readme")"
terminal_two="$(awk '
	/^### Terminal 2:/ { capture=1; next }
	capture && /^### / { exit }
	capture { print }
' "$readme")"

if [[ -z "$quickstart" ]] || ! grep -q 'starport dev' <<<"$quickstart"; then
	printf 'README quickstart must lead with starport dev\n' >&2
	exit 1
fi
if [[ -z "$terminal_one" ]] || ! grep -q 'starport dev' <<<"$terminal_one" ||
	grep -q 'STARPORT_API_KEY' <<<"$terminal_one"; then
	printf 'README Terminal 1 must own the development server, not client state\n' >&2
	exit 1
fi
if [[ -z "$terminal_two" ]] || ! grep -q 'STARPORT_API_KEY' <<<"$terminal_two" ||
	grep -q 'starport dev' <<<"$terminal_two"; then
	printf 'README Terminal 2 must own client state, not the development server\n' >&2
	exit 1
fi
for required_text in 'in-memory state' 'creates no configuration files' \
	'current Starmap catalog view' '/api/v1/admin/providers/refresh'; do
	if ! grep -qF "$required_text" <<<"$quickstart" &&
		! grep -qF "$required_text" "$readme"; then
		printf 'README is missing the tested quickstart contract: %s\n' "$required_text" >&2
		exit 1
	fi
done

if rg -q 'ghcr\.io/agentstation/starport:[0-9]+\.[0-9]+\.[0-9]+' "$readme"; then
	printf 'README contains a container version that can become stale\n' >&2
	exit 1
fi
for release_text in \
	'STARPORT_VERSION="$(gh release view' \
	'--repo agentstation/starport' \
	'--json tagName' \
	"--jq '.tagName | ltrimstr(\"v\")')\"" \
	'ghcr.io/agentstation/starport:$STARPORT_VERSION'; do
	if ! rg -Fq -- "$release_text" "$readme"; then
		printf 'README is missing dynamic stable-release selection: %s\n' "$release_text" >&2
		exit 1
	fi
done

printf 'PASS README quickstart and dynamic stable-release selection\n'
