#!/usr/bin/env bash

set -euo pipefail

cask="${1:-dist/homebrew/Casks/starport.rb}"
expected_version="${2:-}"

if [[ ! -f "$cask" ]]; then
	printf 'Homebrew cask is missing: %s\n' "$cask" >&2
	exit 1
fi

ruby -c "$cask" >/dev/null
actual_version="$(ruby -e '
  text = File.read(ARGV.fetch(0))
  version = text[/^\s*version\s+"([^"]+)"/, 1]
  abort "cask version is missing" unless version
  print version
' "$cask")"

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
require_text '^[[:space:]]*postflight do$' 'the post-install hook'

if ! grep -Fq 'if OS.mac? && system_command("/usr/bin/xattr", args: ["-h"]).exit_status == 0' "$cask"; then
	printf 'Homebrew cask is missing the macOS-only xattr availability check\n' >&2
	exit 1
fi
if ! grep -Fq 'system_command "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "#{staged_path}/starport"], sudo: false' "$cask"; then
	printf 'Homebrew cask is missing the scoped Starport quarantine hook\n' >&2
	exit 1
fi
if [[ "$(grep -Ec 'xattr|com\.apple\.quarantine' "$cask")" -ne 2 ]]; then
	printf 'Homebrew cask contains an unexpected quarantine operation\n' >&2
	exit 1
fi

if [[ -n "$expected_version" ]]; then
	if [[ "$actual_version" != "$expected_version" ]]; then
		printf 'Homebrew cask version is %s, want %s\n' "$actual_version" "$expected_version" >&2
		exit 1
	fi
fi

if grep -Eq 'sha256 "(no_check|[0]+)"' "$cask"; then
	printf 'Homebrew cask contains an invalid checksum\n' >&2
	exit 1
fi
if grep -Eq 'staged_path\.to_s|#\{staged_path\}["[:space:]]|sudo:[[:space:]]*true|HOMEBREW_PREFIX.*xattr|xattr.*HOMEBREW_PREFIX' "$cask"; then
	printf 'Homebrew cask quarantine removal is broader than the staged Starport binary\n' >&2
	exit 1
fi

printf 'PASS Homebrew cask syntax, platforms, checksums, installed artifacts, and scoped hook\n'
