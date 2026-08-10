#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temporary_parent="${TMPDIR:-/tmp}"
temporary_parent="${temporary_parent%/}"
test_root="$(mktemp -d "$temporary_parent/starport-homebrew-publisher.XXXXXX")"

cleanup() {
	case "$test_root" in
		"$temporary_parent"/starport-homebrew-publisher.*) rm -rf "$test_root" ;;
	esac
}
trap cleanup EXIT INT TERM

write_cask() {
	local path=$1
	local version=$2
	local checksum=$3
	mkdir -p "$(dirname "$path")"
	sed \
		-e "s/@VERSION@/$version/g" \
		-e "s/@CHECKSUM@/$checksum/g" \
		"$repository_root/scripts/testdata/starport-cask.rb.tmpl" >"$path"
}

remote="$test_root/tap.git"
seed="$test_root/seed"
candidate="$test_root/candidate.rb"
git init --bare --quiet "$remote"
git clone --quiet "$remote" "$seed" 2>/dev/null
git -C "$seed" switch --quiet -c main
git -C "$seed" config user.name starport-test
git -C "$seed" config user.email starport-test@example.invalid

write_cask "$seed/Casks/starport.rb" 2.0.0 \
	aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
git -C "$seed" add Casks/starport.rb
git -C "$seed" commit --quiet -m "Add Starport 2.0.0"
git -C "$seed" push --quiet --set-upstream origin main

write_cask "$candidate" 1.9.0 \
	bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
"$repository_root/scripts/publish-homebrew-cask.sh" "$candidate" "file://$remote" >/dev/null
test "$(git --git-dir="$remote" show main:Casks/starport.rb | sed -n 's/^[[:space:]]*version "\([^"]*\)"/\1/p')" = 2.0.0

write_cask "$candidate" 2.0.0 \
	cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
if "$repository_root/scripts/publish-homebrew-cask.sh" "$candidate" "file://$remote" >/dev/null 2>&1; then
	printf 'publisher accepted different content for one version\n' >&2
	exit 1
fi

write_cask "$candidate" 2.1.0+build.1 \
	dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd
"$repository_root/scripts/verify-homebrew-cask.sh" "$candidate" 2.1.0+build.1 >/dev/null
"$repository_root/scripts/publish-homebrew-cask.sh" "$candidate" "file://$remote" >/dev/null
"$repository_root/scripts/publish-homebrew-cask.sh" "$candidate" "file://$remote" >/dev/null
test "$(git --git-dir="$remote" show main:Casks/starport.rb | sed -n 's/^[[:space:]]*version "\([^"]*\)"/\1/p')" = 2.1.0+build.1

printf 'PASS monotonic and idempotent Homebrew publication\n'
