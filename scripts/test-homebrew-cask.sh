#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temporary_parent="${TMPDIR:-/tmp}"
test_root="$(mktemp -d "$temporary_parent/starport-homebrew-cask.XXXXXX")"
cleanup() {
	case "$test_root" in
		"$temporary_parent"/starport-homebrew-cask.*) rm -rf "$test_root" ;;
		*) printf 'refusing to remove unexpected test path: %s\n' "$test_root" >&2 ;;
	esac
}
trap cleanup EXIT

valid="$test_root/valid.rb"
broad="$test_root/broad.rb"
sudo="$test_root/sudo.rb"
unguarded="$test_root/unguarded.rb"
cp "$repository_root/scripts/testdata/starport-cask.rb.tmpl" "$valid"

"$repository_root/scripts/verify-homebrew-cask.sh" "$valid" >/dev/null

sed 's|#{staged_path}/starport|#{staged_path}|' "$valid" > "$broad"
if "$repository_root/scripts/verify-homebrew-cask.sh" "$broad" >/dev/null 2>&1; then
	printf 'Homebrew cask verifier accepted a broad staged path\n' >&2
	exit 1
fi

sed 's/sudo: false/sudo: true/' "$valid" > "$sudo"
if "$repository_root/scripts/verify-homebrew-cask.sh" "$sudo" >/dev/null 2>&1; then
	printf 'Homebrew cask verifier accepted a privileged hook\n' >&2
	exit 1
fi

sed 's/if OS\.mac? && /if /' "$valid" > "$unguarded"
if "$repository_root/scripts/verify-homebrew-cask.sh" "$unguarded" >/dev/null 2>&1; then
	printf 'Homebrew cask verifier accepted a cross-platform xattr hook\n' >&2
	exit 1
fi

printf 'PASS scoped Homebrew cask hook regression tests\n'
