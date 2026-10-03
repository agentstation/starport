#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
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
docs_entries="$(mktemp "${TMPDIR:-/tmp}/starport-docs-entries.XXXXXX")"
docs_extract="$(mktemp -d "${TMPDIR:-/tmp}/starport-docs-archive.XXXXXX")"
trap 'rm -f "$expected_names" "$actual_names" "$archive_names" "$docs_entries"; rm -rf "$docs_extract"' EXIT

for platform in darwin_arm64 linux_arm64 linux_x86_64; do
	archive="starport_${version}_${platform}.tar.gz"
	printf '%s\n%s\n' "$archive" "$archive.sbom.json" >>"$expected_names"
	printf '%s\n' "$archive" >>"$archive_names"
done
for platform in windows_arm64 windows_x86_64; do
	archive="starport_${version}_${platform}.zip"
	printf '%s\n%s\n' "$archive" "$archive.sbom.json" >>"$expected_names"
	printf '%s\n' "$archive" >>"$archive_names"
done

docs_root="starport-docs-v${version}"
docs_archive="${docs_root}.tar.gz"
printf '%s\n' "$docs_archive" >>"$expected_names"

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

# The docs archive holds the embedded documentation site below one root
# directory. A checkout that built the site must match it exactly. A
# recovery checkout has no build output, so it checks the layout only.
tar -tzf "$distribution_directory/$docs_archive" >"$docs_entries"
if awk -v root="$docs_root" '
	$0 != root && $0 != root "/" && (index($0, root "/") != 1 || $0 ~ /(^|\/)\.\.(\/|$)/) { unsafe = 1; print }
	END { exit !unsafe }
' "$docs_entries" >&2; then
	printf 'docs archive has an entry outside %s/\n' "$docs_root" >&2
	exit 1
fi
tar -xzf "$distribution_directory/$docs_archive" -C "$docs_extract"
for page in index.html manifest.json; do
	if [ ! -f "$docs_extract/$docs_root/$page" ]; then
		printf 'docs archive is missing %s/%s\n' "$docs_root" "$page" >&2
		exit 1
	fi
done
docs_source="$repository_root/internal/console/dist/docs"
if [ -f "$docs_source/index.html" ] && ! diff -r "$docs_source" "$docs_extract/$docs_root"; then
	printf 'docs archive differs from %s\n' "$docs_source" >&2
	exit 1
fi

printf 'PASS 5 release archives, 5 Syft SBOMs, the docs archive, and the checksum manifest\n'
