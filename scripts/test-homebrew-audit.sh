#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temporary_parent="${TMPDIR:-/tmp}"
temporary_parent="${temporary_parent%/}"
test_root="$(mktemp -d "$temporary_parent/starport-homebrew-audit.XXXXXX")"

cleanup() {
	case "$test_root" in
		"$temporary_parent"/starport-homebrew-audit.*) rm -rf "$test_root" ;;
	esac
}
trap cleanup EXIT INT TERM

mkdir -p "$test_root/bin" "$test_root/tap"
cp "$repository_root/scripts/testdata/starport-cask.rb.tmpl" "$test_root/starport.rb"
install -m 0755 "$repository_root/scripts/testdata/brew-audit-stub" "$test_root/bin/brew"

export STARPORT_AUDIT_TEST_ROOT="$test_root"
export PATH="$test_root/bin:$PATH"

"$repository_root/scripts/audit-homebrew-cask.sh" "$test_root/starport.rb" >/dev/null
test -f "$test_root/tap/Casks/starport.rb"

printf 'PASS repository-free Homebrew audit tap\n'
