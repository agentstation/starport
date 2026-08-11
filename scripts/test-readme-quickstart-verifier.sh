#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temporary_parent="${TMPDIR:-/tmp}"
test_root="$(mktemp -d "$temporary_parent/starport-readme-verifier.XXXXXX")"

cleanup() {
	case "$test_root" in
	"$temporary_parent"/starport-readme-verifier.*) rm -rf "$test_root" ;;
	*) printf 'refusing to remove unexpected test path: %s\n' "$test_root" >&2 ;;
	esac
}
trap cleanup EXIT

verify() {
	STARPORT_README_ROOT="$1" \
		"$repository_root/scripts/verify-readme-quickstart.sh" >/dev/null
}

expect_failure() {
	if verify "$1" 2>/dev/null; then
		printf 'README verifier accepted invalid fixture: %s\n' "$2" >&2
		exit 1
	fi
}

valid="$test_root/valid"
mkdir "$valid"
cp "$repository_root/README.md" "$valid/README.md"
verify "$valid"

obsolete_variable="$test_root/obsolete-variable"
cp -R "$valid" "$obsolete_variable"
printf '\n`STARPORT_BIN`\n' >>"$obsolete_variable/README.md"
expect_failure "$obsolete_variable" obsolete-variable

provider_init="$test_root/provider-init"
cp -R "$valid" "$provider_init"
printf '\n`starport init --provider openai`\n' >>"$provider_init/README.md"
expect_failure "$provider_init" provider-init

reversed_terminals="$test_root/reversed-terminals"
cp -R "$valid" "$reversed_terminals"
sed -i.bak 's/starport dev/echo waiting/g' "$reversed_terminals/README.md"
rm "$reversed_terminals/README.md.bak"
expect_failure "$reversed_terminals" reversed-terminals

stale_release="$test_root/stale-release"
cp -R "$valid" "$stale_release"
printf '\n`ghcr.io/agentstation/starport:1.2.2`\n' >>"$stale_release/README.md"
expect_failure "$stale_release" stale-release

printf 'PASS README quickstart verifier regression tests\n'
