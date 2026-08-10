#!/usr/bin/env bash

set -euo pipefail

cask="${1:-dist/homebrew/Casks/starport.rb}"
expected_version="${2:-}"

if [[ ! -f "$cask" ]]; then
	printf 'Homebrew cask is missing: %s\n' "$cask" >&2
	exit 1
fi

ruby -c "$cask" >/dev/null

require_text() {
	local pattern="$1"
	local description="$2"
	if ! grep -Eq "$pattern" "$cask"; then
		printf 'Homebrew cask is missing %s\n' "$description" >&2
		exit 1
	fi
}

require_text '^cask "starport" do$' 'the canonical cask name'
require_text '^[[:space:]]*binary "starport"$' 'the Starport binary artifact'
require_text '^[[:space:]]*manpage "manpages/starport\.1"$' 'the section-1 manual artifact'
require_text '^[[:space:]]*bash_completion "completions/starport\.bash"$' 'Bash completion'
require_text '^[[:space:]]*zsh_completion "completions/starport\.zsh"$' 'Zsh completion'
require_text '^[[:space:]]*fish_completion "completions/starport\.fish"$' 'Fish completion'
require_text '^[[:space:]]*on_arm do$' 'ARM archives'
require_text '^[[:space:]]*on_intel do$' 'x86-64 archives'
require_text '^[[:space:]]*on_macos do$' 'macOS archives'
require_text '^[[:space:]]*on_linux do$' 'Linux archives'

if [[ -n "$expected_version" ]]; then
	require_text "^[[:space:]]*version \"${expected_version//./\\.}\"$" "version $expected_version"
fi

if grep -Eq 'sha256 "(no_check|[0]+)"' "$cask"; then
	printf 'Homebrew cask contains an invalid checksum\n' >&2
	exit 1
fi
if grep -Eqi 'xattr|quarantine' "$cask"; then
	printf 'Homebrew cask must not bypass macOS Gatekeeper\n' >&2
	exit 1
fi

printf 'PASS Homebrew cask syntax, platforms, checksums, and installed artifacts\n'
