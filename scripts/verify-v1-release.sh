#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
passed=0
failed=0

pass() {
	printf 'PASS %s\n' "$1"
	passed=$((passed + 1))
}

fail() {
	printf 'FAIL %s\n' "$1" >&2
	failed=$((failed + 1))
}

require_file() {
	if [ -f "$repository_root/$1" ]; then
		pass "$1 exists"
	else
		fail "$1 is missing"
	fi
}

require_file .goreleaser.yaml
require_file .github/workflows/release.yaml
require_file .env.example
require_file SECURITY.md

if [ ! -e "$repository_root/docs/PLAN.md" ]; then
	pass 'stale docs/PLAN.md is absent'
else
	fail 'stale docs/PLAN.md remains'
fi

if ! grep -q 'The active v1 plan and its proof files' "$repository_root/docs/ARCHITECTURE.md"; then
	pass 'architecture has no stale active-plan claim'
else
	fail 'architecture retains the stale active-plan claim'
fi

if ! grep -q '^Status: pre-release' "$repository_root/README.md"; then
	pass 'README has no pre-release status'
else
	fail 'README retains pre-release status'
fi

unpinned_actions="$({ grep -hE '^[[:space:]]*uses:' "$repository_root"/.github/workflows/*.yml "$repository_root"/.github/workflows/*.yaml 2>/dev/null || true; } | grep -vE 'uses:[[:space:]]+\./|uses:[[:space:]]+[^@[:space:]]+@[0-9a-f]{40}[[:space:]]*#[[:space:]]*\S+' || true)"
if [ -z "$unpinned_actions" ]; then
	pass 'workflow actions use reviewed commit pins'
else
	fail 'workflow actions include mutable or undocumented references'
fi

if grep -Eq '^\s*github.com/agentstation/starmap v[0-9]+\.[0-9]+\.[0-9]+([-.][^[:space:]]+)?$' "$repository_root/go.mod" && ! grep -Eq '^replace[[:space:]]|^replace[[:space:]]*\(' "$repository_root/go.mod"; then
	pass 'Starport uses a published Starmap version without replacement'
else
	fail 'Starmap dependency is not the approved published version'
fi

if grep -q 'smoke_openrouter_python.py' "$repository_root/scripts/smoke-openrouter-sdks.sh" &&
	grep -q 'smoke_openrouter_typescript.mjs' "$repository_root/scripts/smoke-openrouter-sdks.sh" &&
	grep -q 'smoke_openrouter_go' "$repository_root/scripts/smoke-openrouter-sdks.sh" &&
	! grep -q 'UNVERIFIED' "$repository_root/scripts/smoke-openrouter-sdks.sh"; then
	pass 'release smoke requires all official OpenRouter SDKs'
else
	fail 'release smoke does not require all official OpenRouter SDKs'
fi

if grep -q 'scripts/verify-v1-release.sh' "$repository_root/.github/workflows/ci.yml"; then
	pass 'CI invokes the v1 release verifier'
else
	fail 'CI does not invoke the v1 release verifier'
fi

if grep -q 'syft-version: v1\.51\.0' "$repository_root/.github/workflows/ci.yml" &&
	grep -q 'SYFT_VERSION: v1\.51\.0' "$repository_root/.github/workflows/release.yaml"; then
	pass 'CI and release publication use the reviewed Syft version'
else
	fail 'CI and release publication do not use the same reviewed Syft version'
fi

if [ -f "$repository_root/.goreleaser.yaml" ] &&
	grep -q 'linux' "$repository_root/.goreleaser.yaml" &&
	grep -q 'darwin' "$repository_root/.goreleaser.yaml" &&
	grep -q 'windows' "$repository_root/.goreleaser.yaml" &&
	grep -q 'amd64' "$repository_root/.goreleaser.yaml" &&
	grep -q 'arm64' "$repository_root/.goreleaser.yaml"; then
	pass 'release configuration covers the v1 platform matrix'
else
	fail 'release configuration does not cover the v1 platform matrix'
fi

if grep -Eq 'main\.buildTime=\{\{[[:space:]]*\.CommitDate[[:space:]]*\}\}' \
	"$repository_root/.goreleaser.yaml"; then
	pass 'release binaries use reproducible commit-time metadata'
else
	fail 'release binaries use per-run or missing build-time metadata'
fi

if grep -Eq '^[[:space:]]*-[[:space:]]+-buildvcs=true$' \
	"$repository_root/.goreleaser.yaml"; then
	pass 'release binaries preserve Go VCS provenance'
else
	fail 'release binaries omit Go VCS provenance'
fi

if grep -Eq 'GOCACHE="\$\$release_cache"[[:space:]]+goreleaser release' \
	"$repository_root/Makefile"; then
	pass 'release snapshots isolate mutation-test build cache state'
else
	fail 'release snapshots can reuse mutation-test build cache state'
fi

if [ -f "$repository_root/.github/workflows/release.yaml" ] &&
	grep -q 'Verify draft release assets before publication' "$repository_root/.github/workflows/release.yaml" &&
	grep -q 'attest-build-provenance' "$repository_root/.github/workflows/release.yaml" &&
	grep -q 'immutable' "$repository_root/.github/workflows/release.yaml"; then
	pass 'release workflow verifies provenance before immutable publication'
else
	fail 'release workflow lacks the draft verification and provenance contract'
fi

printf 'Summary: %s passed, %s failed\n' "$passed" "$failed"
test "$failed" -eq 0
