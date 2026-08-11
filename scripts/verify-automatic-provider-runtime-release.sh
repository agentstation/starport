#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repository="agentstation/starport"
release_tag="${STARPORT_AUTOMATIC_PROVIDER_RELEASE_TAG:-v1.0.3}"
version="${release_tag#v}"
container_image="ghcr.io/agentstation/starport"
temporary_parent="${TMPDIR:-/tmp}"
work_root=""
server_pid=""

fail() {
	printf '%s\n' "$1" >&2
	exit 1
}

require_equal() {
	local actual="$1"
	local expected="$2"
	local description="$3"
	if [[ "$actual" != "$expected" ]]; then
		printf '%s: expected %s, got %s\n' "$description" "$expected" "$actual" >&2
		exit 1
	fi
}

cleanup() {
	if [[ -n "$server_pid" ]] && kill -0 "$server_pid" 2>/dev/null; then
		kill -INT "$server_pid" 2>/dev/null || true
		wait "$server_pid" 2>/dev/null || true
	fi
	if [[ -n "$work_root" ]]; then
		case "$work_root" in
		"$temporary_parent"/starport-apr-release.*) rm -rf -- "$work_root" ;;
		*) printf 'refusing to remove unexpected release path: %s\n' "$work_root" >&2 ;;
		esac
	fi
}
trap cleanup EXIT INT TERM

for required_tool in base64 curl docker gh git go grep jq tar; do
	if ! command -v "$required_tool" >/dev/null 2>&1; then
		printf 'automatic provider release verification requires %s\n' "$required_tool" >&2
		exit 1
	fi
done
if [[ ! "$release_tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	printf 'automatic provider release tag is not stable semantic version: %s\n' "$release_tag" >&2
	exit 1
fi

release_json="$(gh api "repos/$repository/releases/tags/$release_tag")"
jq -e '
	.draft == false and
	.prerelease == false and
	.immutable == true
' <<<"$release_json" >/dev/null || fail 'release must be published, stable, and immutable'
require_equal "$(jq -r .tag_name <<<"$release_json")" "$release_tag" \
	'release tag'
require_equal "$(gh release view --repo "$repository" --json tagName --jq .tagName)" \
	"$release_tag" 'latest stable release tag'

tag_ref="$(gh api "repos/$repository/git/ref/tags/$release_tag")"
require_equal "$(jq -r .object.type <<<"$tag_ref")" tag 'release reference type'
tag_object="$(jq -r .object.sha <<<"$tag_ref")"
tag_json="$(gh api "repos/$repository/git/tags/$tag_object")"
require_equal "$(jq -r .object.type <<<"$tag_json")" commit 'annotated tag target type'
release_commit="$(jq -r .object.sha <<<"$tag_json")"
compare_status="$(gh api "repos/$repository/compare/$release_tag...main" --jq .status)"
if [[ "$compare_status" != identical && "$compare_status" != ahead ]]; then
	printf 'release tag is not an ancestor of main: %s\n' "$compare_status" >&2
	exit 1
fi

expected_assets="$({
	printf '%s\n' checksums.txt
	for platform in darwin_arm64 darwin_x86_64 linux_arm64 linux_x86_64; do
		printf 'starport_%s_%s.tar.gz\n' "$version" "$platform"
		printf 'starport_%s_%s.tar.gz.sbom.json\n' "$version" "$platform"
	done
	for platform in windows_arm64 windows_x86_64; do
		printf 'starport_%s_%s.zip\n' "$version" "$platform"
		printf 'starport_%s_%s.zip.sbom.json\n' "$version" "$platform"
	done
} | LC_ALL=C sort)"
actual_assets="$(jq -r '.assets[].name' <<<"$release_json" | LC_ALL=C sort)"
if [[ "$actual_assets" != "$expected_assets" ]]; then
	printf 'public release asset set is not exact\n' >&2
	diff -u <(printf '%s\n' "$expected_assets") <(printf '%s\n' "$actual_assets") || true
	exit 1
fi

release_attestation="$(gh release verify "$release_tag" --repo "$repository" --format json)"
jq -e --arg tag "$release_tag" --arg tag_object "$tag_object" '
	.verificationResult.statement.predicateType ==
		"https://in-toto.io/attestation/release/v0.2" and
	.verificationResult.statement.predicate.repository == "agentstation/starport" and
	.verificationResult.statement.predicate.tag == $tag and
	(.verificationResult.statement.subject | length) == 14 and
	.verificationResult.statement.subject[0].uri ==
		("pkg:github/agentstation/starport@" + $tag) and
	.verificationResult.statement.subject[0].digest.sha1 == $tag_object
' <<<"$release_attestation" >/dev/null || fail 'release attestation contract does not match the release'

release_runs="$(gh run list \
	--repo "$repository" \
	--workflow Release \
	--event push \
	--branch "$release_tag" \
	--limit 10 \
	--json databaseId,headBranch,headSha,status,conclusion,url)"
release_run_id="$(jq -er --arg commit "$release_commit" --arg tag "$release_tag" '
	[.[] | select(
		.headSha == $commit and
		.headBranch == $tag and
		.status == "completed" and
		.conclusion == "success"
	)] | if length == 1 then .[0].databaseId else error("release run is not uniquely successful") end
' <<<"$release_runs")"
run_json="$(gh run view "$release_run_id" --repo "$repository" \
	--json event,headBranch,headSha,status,conclusion,url,jobs)"
jq -e --arg commit "$release_commit" --arg tag "$release_tag" '
	.event == "push" and
	.headBranch == $tag and
	.headSha == $commit and
	.status == "completed" and
	.conclusion == "success" and
	([.jobs[] | select(
		.name == "Release Gate" or
		.name == "Assemble, Verify, and Publish" or
		.name == "Verify Homebrew (macos-latest)" or
		.name == "Verify Homebrew (ubuntu-latest)"
	) | select(.status == "completed" and .conclusion == "success")] | length) == 4
' <<<"$run_json" >/dev/null || fail 'successful release workflow jobs do not match the release'

work_root="$(mktemp -d "$temporary_parent/starport-apr-release.XXXXXX")"
release_directory="$work_root/release"
native_directory="$work_root/native"
mkdir -p "$release_directory" "$native_directory"
gh release download "$release_tag" --repo "$repository" --dir "$release_directory"

(
	cd "$release_directory"
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum --check checksums.txt
	else
		shasum -a 256 --check checksums.txt
	fi
) >/dev/null || fail 'release asset checksums do not match checksums.txt'

goos="$(go env GOOS)"
goarch="$(go env GOARCH)"
case "$goarch" in
amd64) archive_arch=x86_64 ;;
arm64) archive_arch=arm64 ;;
*) printf 'unsupported native release architecture: %s\n' "$goarch" >&2; exit 1 ;;
esac
case "$goos" in
darwin | linux) ;;
*) printf 'unsupported native release operating system: %s\n' "$goos" >&2; exit 1 ;;
esac
native_archive="$release_directory/starport_${version}_${goos}_${archive_arch}.tar.gz"
tar -xzf "$native_archive" -C "$native_directory"
native_binary="$native_directory/starport"
require_equal "$($native_binary --version)" "starport version $version" \
	'native binary version'
if ! "$native_binary" dev --help >/dev/null 2>&1; then
	fail 'released binary does not expose the provider-neutral dev command'
fi
STARPORT_README_ROOT="$native_directory" "$repository_root/scripts/verify-readme-quickstart.sh" >/dev/null

while read -r _ asset; do
	if [[ ! "$asset" =~ ^[A-Za-z0-9._+-]+$ ]]; then
		printf 'release asset has an unsafe name: %s\n' "$asset" >&2
		exit 1
	fi
	gh release verify-asset "$release_tag" "$release_directory/$asset" \
		--repo "$repository" >/dev/null ||
		fail "release attestation does not contain $asset"
	gh attestation verify "$release_directory/$asset" \
		--repo "$repository" \
		--signer-workflow "$repository/.github/workflows/release.yaml" \
		--source-digest "$release_commit" \
		--deny-self-hosted-runners >/dev/null ||
		fail "build provenance is invalid for $asset"
done <"$release_directory/checksums.txt"
gh release verify-asset "$release_tag" "$release_directory/checksums.txt" \
	--repo "$repository" >/dev/null ||
	fail 'release attestation does not contain checksums.txt'
gh attestation verify "$release_directory/checksums.txt" \
	--repo "$repository" \
	--signer-workflow "$repository/.github/workflows/release.yaml" \
	--source-digest "$release_commit" \
	--deny-self-hosted-runners >/dev/null ||
	fail 'build provenance is invalid for checksums.txt'

smoke_port="${STARPORT_RELEASE_SMOKE_PORT:-18087}"
if [[ ! "$smoke_port" =~ ^[0-9]+$ ]] || ((smoke_port < 1 || smoke_port > 65535)); then
	printf 'STARPORT_RELEASE_SMOKE_PORT must be an integer from 1 through 65535\n' >&2
	exit 1
fi
development_log="$work_root/development.log"
env -i \
	"PATH=${PATH:-/usr/bin:/bin}" \
	"STARPORT_CONFIG_DIR=$work_root/config" \
	"STARPORT_SERVER_PORT=$smoke_port" \
	"$native_binary" dev >"$development_log" 2>&1 &
server_pid=$!
ready=false
for _ in {1..60}; do
	if curl --connect-timeout 1 --max-time 2 --fail --silent \
		"http://127.0.0.1:$smoke_port/health/ready" >/dev/null; then
		ready=true
		break
	fi
	if ! kill -0 "$server_pid" 2>/dev/null; then
		break
	fi
	sleep 0.25
done
if [[ "$ready" != true ]]; then
	printf 'released development gateway did not become ready\n' >&2
	sed -n '1,120p' "$development_log" >&2
	exit 1
fi
gateway_key="$(sed -n 's/^Gateway API key (shown once): //p' "$development_log" | head -n 1)"
[[ -n "$gateway_key" ]] || fail 'released development gateway did not print its API key'
curl --connect-timeout 2 --max-time 10 --fail --silent \
	-H "Authorization: Bearer $gateway_key" \
	"http://127.0.0.1:$smoke_port/api/v1/admin/providers" |
	jq -e '.providers | type == "array"' >/dev/null
kill -INT "$server_pid"
wait "$server_pid"
server_pid=""
[[ ! -e "$work_root/config" ]] || fail 'released development gateway persisted local state'

cask="$work_root/starport.rb"
gh api 'repos/agentstation/homebrew-tap/contents/Casks/starport.rb?ref=main' \
	--jq .content | base64 --decode >"$cask"
"$repository_root/scripts/verify-homebrew-cask.sh" "$cask" "$version" >/dev/null
for platform in darwin_arm64 darwin_x86_64 linux_arm64 linux_x86_64; do
	archive="starport_${version}_${platform}.tar.gz"
	digest="$(awk -v asset="$archive" '$2 == asset { print $1 }' \
		"$release_directory/checksums.txt")"
	[[ -n "$digest" ]] || fail "release checksum is missing for $archive"
	grep -Fq "sha256 \"$digest\"" "$cask" ||
		fail "Homebrew cask does not contain the release checksum for $archive"
done

container_digest="$(docker buildx imagetools inspect "$container_image:$version" \
	--format '{{json .Manifest}}' | jq -r .digest)"
"$repository_root/scripts/verify-release-container.sh" \
	"$container_image" "$version" "$container_digest" >/dev/null
gh attestation verify "oci://$container_image@$container_digest" \
	--repo "$repository" \
	--signer-workflow "$repository/.github/workflows/release.yaml" \
	--source-digest "$release_commit" \
	--deny-self-hosted-runners >/dev/null

printf 'PASS immutable %s automatic-provider release at %s with 13 assets, cask, and container %s\n' \
	"$release_tag" "$release_commit" "$container_digest"
