#!/usr/bin/env bash

set -euo pipefail

cask="${1:-dist/homebrew/Casks/starport.rb}"
tap="agentstation/starport-audit"

if [[ ! -f "$cask" ]]; then
	printf 'Homebrew cask is missing: %s\n' "$cask" >&2
	exit 1
fi
if ! command -v brew >/dev/null 2>&1; then
	printf 'Homebrew is required to audit the generated cask\n' >&2
	exit 1
fi
if brew tap | grep -Fxq "$tap"; then
	printf 'temporary audit tap already exists: %s\n' "$tap" >&2
	exit 1
fi

cask="$(cd "$(dirname "$cask")" && pwd)/$(basename "$cask")"
brew tap-new --no-git "$tap" >/dev/null
trap 'brew untap "$tap" >/dev/null' EXIT

tap_root="$(brew --repository "$tap")"
mkdir -p "$tap_root/Casks"
install -m 0644 "$cask" "$tap_root/Casks/starport.rb"
brew audit --cask --strict "$tap/starport"

printf 'PASS Homebrew strict cask audit\n'
