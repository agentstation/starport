#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
workflow="$repository_root/.github/workflows/release.yaml"
goreleaser="$repository_root/.goreleaser.yaml"

require_text() {
	local pattern="$1"
	local description="$2"
	if ! grep -Eq "$pattern" "$workflow"; then
		printf 'release workflow is missing %s\n' "$description" >&2
		exit 1
	fi
}

require_goreleaser_text() {
	local pattern="$1"
	local description="$2"
	if ! grep -Eq "$pattern" "$goreleaser"; then
		printf 'GoReleaser configuration is missing %s\n' "$description" >&2
		exit 1
	fi
}

if [ ! -f "$workflow" ]; then
	printf 'release workflow is missing: %s\n' "$workflow" >&2
	exit 1
fi

require_text '^permissions:$' 'a default permission boundary'
require_text '^[[:space:]]+contents: read$' 'read-only default contents permission'
require_text 'git merge-base --is-ancestor .* origin/main' 'a main ancestry check'
require_text 'test .*origin/main' 'an exact main-head check'
require_text 'smoke-openrouter-sdks\.sh' 'the required official SDK gate'
require_text 'verify-release-binaries\.sh' 'the portable binary gate'
require_text 'verify-release-archives\.sh' 'the archive and SBOM gate'
require_text 'attest-build-provenance@4d101475d8b20a2381f78447822ac1eab6504dd8' 'the pinned provenance action'
require_text 'Verify draft release assets before publication' 'draft asset verification'
require_text 'DRAFT_RELEASES=' 'bounded draft release lookup'
require_text 'select\(.tag_name == \$tag and .draft == true\)' 'draft-only release selection'
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
require_text 'Publish the generated Homebrew cask' 'post-verification Homebrew publication'
require_text '!contains\(github\.ref_name, .-rc\..\)' 'stable release Homebrew publication'
require_text '!contains\(inputs\.tag, .-rc\..\)' 'stable recovery Homebrew publication'
require_text 'git@github.com:agentstation/homebrew-tap\.git[[:space:]\\]+$' 'the canonical Homebrew tap'
require_text '^[[:space:]]+main$' 'the Homebrew tap main branch'
require_text 'homebrew_version:.*steps\.homebrew\.outputs\.version' 'the effective Homebrew version output'
require_text 'HOMEBREW_VERSION:.*needs\.release\.outputs\.homebrew_version.*needs\.recover\.outputs\.homebrew_version' 'effective Homebrew version selection'
require_text 'starport --version.*HOMEBREW_VERSION' 'effective Homebrew version verification'
require_text '^  verify-homebrew:' 'macOS and Linux Homebrew installation verification'
require_text 'brew install agentstation/tap/starport' 'the documented Homebrew install command'
require_text 'MACOS_SIGN_P12' 'the Developer ID signing credential'
require_text 'MACOS_NOTARY_ISSUER_ID' 'the Apple notarization credential'
require_text 'xattr -p com\.apple\.quarantine.*STARPORT_BINARY' 'installed quarantine verification'
require_goreleaser_text 'branch:[[:space:]]+main' 'the Homebrew tap main branch'
for credential in \
	MACOS_SIGN_P12 \
	MACOS_SIGN_PASSWORD \
	MACOS_NOTARY_KEY \
	MACOS_NOTARY_KEY_ID \
	MACOS_NOTARY_ISSUER_ID; do
	require_goreleaser_text "enabled:.*isEnvSet.*$credential" "conditional macOS signing credential $credential"
done

if grep -q 'Require Apple signing and notarization credentials' "$workflow"; then
	printf 'release workflow has a mandatory Apple credential gate\n' >&2
	exit 1
fi
if grep -Fq 'RELEASE_JSON=$(gh api "repos/$GITHUB_REPOSITORY/releases/tags/$GITHUB_REF_NAME")' "$workflow"; then
	printf 'release workflow reads an untagged draft through the tag endpoint\n' >&2
	exit 1
fi

verification_line="$(grep -n -m1 'Verify draft release assets before publication' "$workflow" | cut -d: -f1)"
publication_line="$(grep -n -m1 'Publish verified immutable release' "$workflow" | cut -d: -f1)"
promotion_line="$(grep -n -m1 'Promote verified container tags' "$workflow" | cut -d: -f1)"
if [ "$verification_line" -ge "$publication_line" ] || [ "$publication_line" -ge "$promotion_line" ]; then
	printf 'draft verification, release publication, and container tag promotion are out of order\n' >&2
	exit 1
fi

published_verification_line="$(grep -n -m1 'Verify published release and publisher identity' "$workflow" | cut -d: -f1)"
homebrew_publication_line="$(grep -n -m1 'Publish the generated Homebrew cask' "$workflow" | cut -d: -f1)"
if [ "$promotion_line" -ge "$published_verification_line" ] ||
	[ "$published_verification_line" -ge "$homebrew_publication_line" ]; then
	printf 'container promotion, published-release verification, and Homebrew publication are out of order\n' >&2
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

recovery_immutable_line="$(grep -n -m1 'Verify recovered immutable release' "$workflow" | cut -d: -f1)"
recovery_homebrew_line="$(grep -n 'Publish the generated Homebrew cask' "$workflow" | sed -n '2p' | cut -d: -f1)"
if [ "$recovery_promotion_line" -ge "$recovery_immutable_line" ] ||
	[ "$recovery_immutable_line" -ge "$recovery_homebrew_line" ]; then
	printf 'recovered container promotion, immutable verification, and Homebrew publication are out of order\n' >&2
	exit 1
fi

if grep -Eq 'pull_request_target|permissions:[[:space:]]*write-all|uses:.*@(v[0-9]|main|master|latest)' "$workflow"; then
	printf 'release workflow contains an unsafe trigger, permission, or mutable action reference\n' >&2
	exit 1
fi

if "$repository_root/scripts/generate-release-artifacts.sh" \
	build/../../release-artifact-escape >/dev/null 2>&1; then
	printf 'release artifact generator accepted an unsafe output path\n' >&2
	exit 1
fi

"$repository_root/scripts/test-homebrew-publisher.sh" >/dev/null
"$repository_root/scripts/test-homebrew-audit.sh" >/dev/null
"$repository_root/scripts/test-homebrew-cask.sh" >/dev/null
"$repository_root/scripts/test-release-container-verifier.sh" >/dev/null

printf 'PASS release workflow contract\n'
