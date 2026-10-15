#!/usr/bin/env bash
# Launch-state verifier for the GitHub, release, and edge facts that only the
# repository owner can change. Read-only. It needs gh (admin token for the
# secret listings) and curl. Usage: verify-launch-state.sh
# Output ends with "Summary: N passed, M failed". Terminal count: 16 passed.
set -u
repo=agentstation/starport
site=https://starport.agentstation.ai
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
api_true() { test "$(gh api "$1" --jq "$2" 2>/dev/null)" = "$3"; }
env_secrets() {
	test "$(gh api "repos/$repo/environments/starport-site/secrets" --jq '[.secrets[].name] as $n | ["CLOUDFLARE_API_TOKEN","CLOUDFLARE_ACCOUNT_ID"] | all(. as $s | $n | index($s) != null)' 2>/dev/null)" = true
}
env_reviewers() {
	test "$(gh api "repos/$repo/environments/starport-site" --jq '[.protection_rules[]? | select(.type=="required_reviewers")] | length' 2>/dev/null)" -ge 1
}
signing_secrets() {
	test "$(gh api "repos/$repo/actions/secrets" --jq '[.secrets[].name] as $n | ["MACOS_SIGN_P12","MACOS_SIGN_PASSWORD","MACOS_NOTARY_KEY","MACOS_NOTARY_KEY_ID","MACOS_NOTARY_ISSUER_ID"] | all(. as $s | $n | index($s) != null)' 2>/dev/null)" = true
}
tag_ruleset() {
	test "$(gh api "repos/$repo/rulesets" --jq '[.[] | select(.target=="tag")] | length' 2>/dev/null)" -ge 1
}
http_redirects() {
	code=$(curl -s -o /dev/null -w '%{http_code}' "http://starport.agentstation.ai/")
	case "$code" in 301|302|307|308) return 0 ;; *) return 1 ;; esac
}
hsts() { curl -sI "$site/" | grep -qi '^strict-transport-security:'; }
latest_tag() { gh release view -R "$repo" --json tagName --jq .tagName 2>/dev/null; }
# The served page names the latest release tag and port 7827, carries no
# pre-1.4 copy, declares a social image, and the sitemap is served.
live_site_current() {
	tag=$(latest_tag)
	body=$(curl -s "$site/")
	test -n "$tag" && test "$tag" != "v1.3.0" &&
		printf '%s' "$body" | grep -q "$tag" &&
		printf '%s' "$body" | grep -q '7827' &&
		! printf '%s' "$body" | grep -q '127.0.0.1:8080' &&
		! printf '%s' "$body" | grep -q 'Temporary by default' &&
		! printf '%s' "$body" | grep -qi 'temporary gateway' &&
		printf '%s' "$body" | grep -q 'og:image' &&
		test "$(curl -s -o /dev/null -w '%{http_code}' "$site/sitemap.xml")" = 200 &&
		curl -s "$site/.well-known/security.txt" | grep -q 'Contact: mailto:security@agentstation.ai'
}
# The latest release is newer than v1.3.0, and the tap cask carries its version.
latest_release_after_v130() {
	tag=$(latest_tag)
	test -n "$tag" && test "$tag" != "v1.3.0" && test "$(printf '%s\nv1.3.0\n' "$tag" | sort -V | tail -1)" = "$tag" || return 1
	cask=$(gh api repos/agentstation/homebrew-tap/contents/Casks/starport.rb --jq .content 2>/dev/null | base64 -d | grep -E '^[[:space:]]*version "' | sed -E 's/.*"([^"]+)".*/\1/')
	test -n "$cask" && test "v$cask" = "$tag"
}
starmap_pin_tagged() {
	gh api "repos/$repo/contents/go.mod" --jq .content 2>/dev/null | base64 -d | grep -Eq 'agentstation/starmap v[0-9]+\.[0-9]+\.[0-9]+$'
}
main_ci_green() {
	test "$(gh run list -R "$repo" -w ci.yml -b main -L 1 --status completed --json conclusion --jq '.[0].conclusion' 2>/dev/null)" = success
}

check "starport-site environment holds both Cloudflare secret names" env_secrets
check "starport-site environment requires reviewers" env_reviewers
check "secret scanning is enabled" api_true "repos/$repo" '.security_and_analysis.secret_scanning.status' enabled
check "push protection is enabled" api_true "repos/$repo" '.security_and_analysis.secret_scanning_push_protection.status' enabled
check "Dependabot security updates are enabled" api_true "repos/$repo" '.security_and_analysis.dependabot_security_updates.status' enabled
check "private vulnerability reporting is enabled" api_true "repos/$repo/private-vulnerability-reporting" '.enabled' true
check "CodeQL default setup is configured" api_true "repos/$repo/code-scanning/default-setup" '.state' configured
check "repository topics are set" bash -c "test \"\$(gh api repos/$repo --jq '.topics | length')\" -ge 3"
check "repository secrets hold the five macOS signing names" signing_secrets
check "a tag ruleset protects release tags" tag_ruleset
check "http redirects to https" http_redirects
check "https sends HSTS" hsts
check "the live site serves the latest release copy, social image, sitemap, and security.txt" live_site_current
check "the latest release is newer than v1.3.0 and the cask matches it" latest_release_after_v130
check "go.mod pins a tagged Starmap release" starmap_pin_tagged
check "the latest completed main CI run is green" main_ci_green

printf 'Summary: %d passed, %d failed\n' "$passed" "$failed"
test "$failed" -eq 0
