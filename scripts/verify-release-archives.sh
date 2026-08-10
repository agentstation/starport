#!/usr/bin/env bash

set -euo pipefail

distribution_directory="${1:-dist}"
metadata_file="$distribution_directory/metadata.json"
checksum_file="$distribution_directory/checksums.txt"

if [ ! -f "$metadata_file" ] || [ ! -f "$checksum_file" ]; then
	printf 'release metadata or checksums are missing from %s\n' \
		"$distribution_directory" >&2
	exit 1
fi

version="${2:-$(jq -r .version "$metadata_file")}"
expected_names="$(mktemp "${TMPDIR:-/tmp}/starport-release-assets.XXXXXX")"
actual_names="$(mktemp "${TMPDIR:-/tmp}/starport-release-checksums.XXXXXX")"
archive_names="$(mktemp "${TMPDIR:-/tmp}/starport-release-archives.XXXXXX")"
trap 'rm -f "$expected_names" "$actual_names" "$archive_names"' EXIT

for platform in darwin_arm64 darwin_x86_64 linux_arm64 linux_x86_64; do
	archive="starport_${version}_${platform}.tar.gz"
	printf '%s\n%s\n' "$archive" "$archive.sbom.json" >>"$expected_names"
	printf '%s\n' "$archive" >>"$archive_names"
done
for platform in windows_arm64 windows_x86_64; do
	archive="starport_${version}_${platform}.zip"
	printf '%s\n%s\n' "$archive" "$archive.sbom.json" >>"$expected_names"
	printf '%s\n' "$archive" >>"$archive_names"
done

awk '{print $2}' "$checksum_file" | LC_ALL=C sort >"$actual_names"
LC_ALL=C sort -o "$expected_names" "$expected_names"
if ! diff -u "$expected_names" "$actual_names"; then
	printf 'release checksum manifest does not contain the exact archive and SBOM set\n' >&2
	exit 1
fi

(
	cd "$distribution_directory"
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum --check checksums.txt
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 --check checksums.txt
	else
		printf 'sha256sum or shasum is required to verify release checksums\n' >&2
		exit 1
	fi
)

while IFS= read -r archive; do
	if [[ ! "$archive" =~ ^[A-Za-z0-9._+-]+$ ]]; then
		printf 'release archive has an unsafe name: %s\n' "$archive" >&2
		exit 1
	fi
	contents="$(mktemp "${TMPDIR:-/tmp}/starport-archive-contents.XXXXXX")"
	if [[ "$archive" == *.zip ]]; then
		unzip -Z1 "$distribution_directory/$archive" | LC_ALL=C sort >"$contents"
		executable=starport.exe
	else
		tar -tzf "$distribution_directory/$archive" | LC_ALL=C sort >"$contents"
		executable=starport
	fi
	if ! diff -u <(
		printf '%s\n' \
			.env.example \
			LICENSE \
			README.md \
			SECURITY.md \
			completions/starport.bash \
			completions/starport.fish \
			completions/starport.ps1 \
			completions/starport.zsh \
			manpages/starport.1 \
			"$executable" |
			LC_ALL=C sort
	) "$contents"; then
		printf 'release archive has unexpected contents: %s\n' "$archive" >&2
		rm -f "$contents"
		exit 1
	fi
	rm -f "$contents"

	sbom="$distribution_directory/$archive.sbom.json"
	if ! jq -e \
		'(.spdxVersion | startswith("SPDX-")) and
		(.packages | type == "array") and
		(.relationships | type == "array")' \
		"$sbom" >/dev/null; then
		printf 'release SBOM is not valid SPDX JSON: %s\n' "$sbom" >&2
		exit 1
	fi
done <"$archive_names"

printf 'PASS 6 release archives, 6 Syft SBOMs, and the checksum manifest\n'
