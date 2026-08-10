#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temporary_parent="${TMPDIR:-/tmp}"
test_root="$(mktemp -d "$temporary_parent/starport-container-verifier.XXXXXX")"
cleanup() {
	case "$test_root" in
		"$temporary_parent"/starport-container-verifier.*) rm -rf "$test_root" ;;
		*) printf 'refusing to remove unexpected test path: %s\n' "$test_root" >&2 ;;
	esac
}
trap cleanup EXIT

index_digest="sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
amd64_digest="sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
arm64_digest="sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
sbom_one="sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
sbom_two="sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"

mkdir "$test_root/bin"
export STARPORT_TEST_INDEX_DIGEST="$index_digest"
export STARPORT_TEST_AMD64_DIGEST="$amd64_digest"
export STARPORT_TEST_ARM64_DIGEST="$arm64_digest"
export STARPORT_TEST_SBOM_ONE="$sbom_one"
export STARPORT_TEST_SBOM_TWO="$sbom_two"
export STARPORT_TEST_DOCKER_LOG="$test_root/docker.log"

cp "$repository_root/scripts/testdata/docker-release-verifier.sh" "$test_root/bin/docker"
chmod +x "$test_root/bin/docker"

PATH="$test_root/bin:$PATH" \
	"$repository_root/scripts/verify-release-container.sh" \
	ghcr.io/agentstation/starport 1.2.3 "$index_digest" >/dev/null

if ! grep -Fq "ghcr.io/agentstation/starport@$amd64_digest --format {{json .Image}}" \
	"$STARPORT_TEST_DOCKER_LOG"; then
	printf 'container verifier did not inspect the AMD64 child image config\n' >&2
	exit 1
fi
if grep -Fq "ghcr.io/agentstation/starport@$index_digest --format {{json .Image}}" \
	"$STARPORT_TEST_DOCKER_LOG"; then
	printf 'container verifier inspected the OCI index as an image config\n' >&2
	exit 1
fi

printf 'PASS platform-child container verification\n'
