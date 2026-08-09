#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
workflow="$repository_root/.github/workflows/release.yaml"

require_text() {
	local pattern="$1"
	local description="$2"
	if ! grep -Eq "$pattern" "$workflow"; then
		printf 'release workflow is missing %s\n' "$description" >&2
		exit 1
	fi
}

if [ ! -f "$workflow" ]; then
	printf 'release workflow is missing: %s\n' "$workflow" >&2
	exit 1
fi

require_text '^permissions:$' 'a default permission boundary'
require_text '^[[:space:]]+contents: read$' 'read-only default contents permission'
require_text 'git merge-base --is-ancestor .* origin/master' 'a master ancestry check'
require_text 'test .*origin/master' 'an exact master-head check'
require_text 'smoke-openrouter-sdks\.sh' 'the required official SDK gate'
require_text 'verify-release-binaries\.sh' 'the portable binary gate'
require_text 'verify-release-archives\.sh' 'the archive and SBOM gate'
require_text 'attest-build-provenance@4d101475d8b20a2381f78447822ac1eab6504dd8' 'the pinned provenance action'
require_text 'Verify draft release assets before publication' 'draft asset verification'
require_text 'Publish verified immutable release' 'one-way release publication'
require_text 'gh attestation verify' 'attestation readback'
require_text 'immutable' 'immutable release readback'
require_text 'source_run_id' 'bounded recovery input'
require_text 'SOURCE_RUN.*conclusion.*failure|conclusion.*SOURCE_RUN.*failure' 'failed-run recovery validation'
require_text 'ghcr\.io/agentstation/starport' 'the canonical GHCR image'
require_text 'container-digest\.txt' 'container digest recovery evidence'
require_text 'find dist .*chmod u\+x' 'recovered executable permission restoration'
require_text 'build-tag=.*sha-.*GITHUB_SHA.*GITHUB_RUN_ID.*GITHUB_RUN_ATTEMPT' 'a unique staging image tag'
require_text 'Promote verified container tags' 'verified canonical image promotion'

verification_line="$(grep -n -m1 'Verify draft release assets before publication' "$workflow" | cut -d: -f1)"
publication_line="$(grep -n -m1 'Publish verified immutable release' "$workflow" | cut -d: -f1)"
promotion_line="$(grep -n -m1 'Promote verified container tags' "$workflow" | cut -d: -f1)"
if [ "$verification_line" -ge "$publication_line" ] || [ "$publication_line" -ge "$promotion_line" ]; then
	printf 'draft verification, release publication, and container tag promotion are out of order\n' >&2
	exit 1
fi

recovery_verification_line="$(grep -n -m1 'Verify exact recovered assets and provenance' "$workflow" | cut -d: -f1)"
recovery_publication_line="$(grep -n 'Publish verified immutable release' "$workflow" | sed -n '2p' | cut -d: -f1)"
recovery_promotion_line="$(grep -n -m1 'Promote verified recovered container tags' "$workflow" | cut -d: -f1)"
if [ "$recovery_verification_line" -ge "$recovery_publication_line" ] ||
	[ "$recovery_publication_line" -ge "$recovery_promotion_line" ]; then
	printf 'recovery verification, release publication, and container tag promotion are out of order\n' >&2
	exit 1
fi

if grep -Eq 'pull_request_target|permissions:[[:space:]]*write-all|uses:.*@(v[0-9]|main|master|latest)' "$workflow"; then
	printf 'release workflow contains an unsafe trigger, permission, or mutable action reference\n' >&2
	exit 1
fi

printf 'PASS release workflow contract\n'
