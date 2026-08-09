#!/usr/bin/env bash

set -euo pipefail

image="${1:-ghcr.io/agentstation/starport}"
version="${2:?usage: verify-release-container.sh IMAGE VERSION DIGEST}"
expected_digest="${3:?usage: verify-release-container.sh IMAGE VERSION DIGEST}"
verification_mode="${4:-canonical-tags}"

if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$ ]]; then
	printf 'container version is not an application semantic version: %s\n' "$version" >&2
	exit 1
fi
if [[ ! "$expected_digest" =~ ^sha256:[0-9a-f]{64}$ ]]; then
	printf 'container digest is not a SHA-256 digest: %s\n' "$expected_digest" >&2
	exit 1
fi

if [ "$verification_mode" = canonical-tags ]; then
	tags=("$version" "v$version")
	if [[ "$version" != *-* ]]; then
		tags+=(latest)
	fi

	for tag in "${tags[@]}"; do
		actual_digest="$(
			docker buildx imagetools inspect "$image:$tag" \
				--format '{{json .Manifest}}' | jq -r .digest
		)"
		if [ "$actual_digest" != "$expected_digest" ]; then
			printf 'container tag %s resolved to %s, want %s\n' \
				"$tag" "$actual_digest" "$expected_digest" >&2
			exit 1
		fi
	done
elif [ "$verification_mode" != digest-only ]; then
	printf 'unknown container verification mode: %s\n' "$verification_mode" >&2
	exit 1
fi

manifest="$(docker buildx imagetools inspect "$image@$expected_digest" --raw)"
platforms="$(
	jq -r \
		'.manifests[] | select(.platform.os != "unknown") | [.platform.os, .platform.architecture] | join("/")' \
		<<<"$manifest" | LC_ALL=C sort -u
)"
if [ "$platforms" != $'linux/amd64\nlinux/arm64' ]; then
	printf 'container platform matrix is unexpected:\n%s\n' "$platforms" >&2
	exit 1
fi

sbom_manifests="$(
	jq '[.manifests[] | select(.platform.os == "unknown")] | length' <<<"$manifest"
)"
if [ "$sbom_manifests" -lt 2 ]; then
	printf 'container manifest has %s attestation manifests; want at least 2 SBOM manifests\n' \
		"$sbom_manifests" >&2
	exit 1
fi

version_output="$(docker run --rm --platform linux/amd64 "$image@$expected_digest" --version)"
if [ "$version_output" != "starport version $version" ]; then
	printf 'container version mismatch: got %q, want %q\n' \
		"$version_output" "starport version $version" >&2
	exit 1
fi

runtime_user="$(docker image inspect "$image@$expected_digest" --format '{{.Config.User}}')"
if [ "$runtime_user" != '65532:65532' ]; then
	printf 'container runtime user is %q, want 65532:65532\n' "$runtime_user" >&2
	exit 1
fi

printf 'PASS version-exact non-root multi-platform container %s@%s\n' \
	"$image" "$expected_digest"
