#!/usr/bin/env bash
# Launch-readiness verifier for the repository files that the plan changes.
# Usage: verify.sh [repository-root]
# Output ends with "Summary: N passed, M failed". Terminal count: 14 passed.
set -u
root="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)}"
cd "$root" || exit 2
passed=0
failed=0
check() {
	local name="$1"
	shift
	if "$@" >/dev/null 2>&1; then
		passed=$((passed + 1))
		printf 'PASS %s\n' "$name"
	else
		failed=$((failed + 1))
		printf 'FAIL %s\n' "$name"
	fi
}
absent() { ! test -e "$1"; }
no_match() { ! grep -Eq "$1" "$2"; }
# Pass when Unreleased has a bullet, or after the release rename, when the
# v1.4.0 section has a "Breaking changes" heading with a bullet below it.
release_notes_present() {
	awk '/^## Unreleased/{u=1; r=0; next} /^## v1\.4\.0/{r=1; u=0; next} /^## /{u=0; r=0} u && /^- /{n++} r && /^### Breaking changes/{b=1} r && b && /^- /{m++} END{exit !(n>0 || m>0)}' CHANGELOG.md
}

check "issue chooser links Discussions (enabled 2026-10-17, D2)" grep -q '/discussions' .github/ISSUE_TEMPLATE/config.yml
check "issue chooser routes security reports to the policy address" grep -Eq 'security@agentstation\.ai|security/advisories' .github/ISSUE_TEMPLATE/config.yml
check "CODEOWNERS names no missing team" no_match '@agentstation/maintainers' .github/CODEOWNERS
check "CHANGELOG lists the changes since v1.3.0 under Unreleased or v1.4.0" release_notes_present
check "README, CHANGELOG, and docs/site carry no plan jargon" bash -c "! grep -rEq 'CSP[0-9]+' README.md CHANGELOG.md docs/site && ! grep -q 'D42' CHANGELOG.md"
check "security posture names the catalog egress opt-out" grep -q 'STARPORT_CATALOG_SOURCE' docs/SECURITY-POSTURE.md
check "site ships a sitemap route" test -e website/src/app/sitemap.ts
check "site layout declares Open Graph metadata" grep -q 'openGraph' website/src/app/layout.tsx
check "Dependabot covers the website package" grep -q '/website' .github/dependabot.yml
check "docs-pages names the canonical documentation host" grep -qi 'canonical' .github/workflows/docs-pages.yaml
check "native-release verifies a current release by default" no_match "'v1\.2\.0'" .github/workflows/native-release.yaml
check "no agent launcher at the repository root" absent spawn-agent.sh
check "the completed product-demos plan is removed" absent docs/plans/product-demos-plan.html
check "release reports missing macOS signing material" grep -q 'Report macOS signing material' .github/workflows/release.yaml

printf 'Summary: %d passed, %d failed\n' "$passed" "$failed"
test "$failed" -eq 0
