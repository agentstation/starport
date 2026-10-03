#!/usr/bin/env bash

# assemble-docs-pages.sh builds the public documentation site from the docs
# release assets. A Pages deployment replaces the whole site, so the script
# reassembles every release that carries a docs archive.
#
#   <out>/<tag>/         the docs archive of each release, unchanged
#   <out>/latest/        a copy of the release that the latest tag names
#   <out>/index.html     a redirect to latest/ with a list of the versions
#   <out>/manifest.json  each version with its archive digest and the
#                        embedded manifest.json of that release, unchanged
#   <out>/.nojekyll      turns off the Pages Jekyll build
#
# The script verifies each archive against the checksums.txt of its release
# and each extracted file against the embedded manifest. It downloads the
# assets with gh. With --assets-dir it reads <dir>/<tag>/ instead and makes
# no network call.

set -euo pipefail

usage() {
	printf 'usage: %s [--assets-dir DIR] [--repository OWNER/REPO] TAG LATEST OUTPUT\n' \
		"$(basename "$0")" >&2
	exit 2
}

assets_directory=""
repository="${GITHUB_REPOSITORY:-agentstation/starport}"
while [ "$#" -gt 0 ]; do
	case "$1" in
	--assets-dir)
		[ "$#" -ge 2 ] || usage
		assets_directory="$2"
		shift 2
		;;
	--repository)
		[ "$#" -ge 2 ] || usage
		repository="$2"
		shift 2
		;;
	--*) usage ;;
	*) break ;;
	esac
done
[ "$#" -eq 3 ] || usage
deploy_tag="$1"
latest_tag="$2"
output_directory="$3"

tag_pattern='^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$'
for tag in "$deploy_tag" "$latest_tag"; do
	if [[ ! "$tag" =~ $tag_pattern ]]; then
		printf 'release tag is not a version tag: %s\n' "$tag" >&2
		exit 1
	fi
done
if [[ ! "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
	printf 'repository is not OWNER/REPO: %s\n' "$repository" >&2
	exit 1
fi
if [ -e "$output_directory" ] && [ -n "$(ls -A "$output_directory")" ]; then
	printf 'output directory is not empty: %s\n' "$output_directory" >&2
	exit 1
fi

for required_tool in jq tar; do
	if ! command -v "$required_tool" >/dev/null 2>&1; then
		printf 'assemble-docs-pages requires %s\n' "$required_tool" >&2
		exit 1
	fi
done

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	else
		shasum -a 256 "$1" | awk '{print $1}'
	fi
}

sha256_check() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum --check --status "$1"
	else
		shasum -a 256 --check --status "$1"
	fi
}

work_directory="$(mktemp -d "${TMPDIR:-/tmp}/starport-docs-pages.XXXXXX")"
trap 'rm -rf "$work_directory"' EXIT
tags_file="$work_directory/tags"

if [ -n "$assets_directory" ]; then
	for tag_directory in "$assets_directory"/*/; do
		tag="$(basename "$tag_directory")"
		if [ -f "$tag_directory/starport-docs-$tag.tar.gz" ]; then
			printf '%s\n' "$tag" >>"$tags_file"
		fi
	done
else
	if ! command -v gh >/dev/null 2>&1; then
		printf 'assemble-docs-pages requires gh to download the release assets\n' >&2
		exit 1
	fi
	assets_directory="$work_directory/assets"
	# A prerelease joins the site only when the deployment names it.
	gh release list --repo "$repository" --exclude-drafts --limit 1000 \
		--json tagName,isPrerelease \
		--jq '.[] | [.tagName, .isPrerelease] | @tsv' |
		while IFS=$'\t' read -r tag prerelease; do
			[[ "$tag" =~ $tag_pattern ]] || continue
			if [ "$prerelease" = "true" ] && [ "$tag" != "$deploy_tag" ] && [ "$tag" != "$latest_tag" ]; then
				continue
			fi
			asset_names="$(gh release view "$tag" --repo "$repository" --json assets --jq '.assets[].name')"
			if grep -xF "starport-docs-$tag.tar.gz" <<<"$asset_names" >/dev/null; then
				mkdir -p "$assets_directory/$tag"
				gh release download "$tag" --repo "$repository" --dir "$assets_directory/$tag" \
					--pattern "starport-docs-$tag.tar.gz" --pattern checksums.txt
				printf '%s\n' "$tag" >>"$tags_file"
			fi
		done
fi

if [ ! -s "$tags_file" ]; then
	printf 'no release carries a docs archive\n' >&2
	exit 1
fi
sort -V -r -o "$tags_file" "$tags_file"
for tag in "$deploy_tag" "$latest_tag"; do
	if ! grep -qxF "$tag" "$tags_file"; then
		printf 'release %s carries no docs archive\n' "$tag" >&2
		exit 1
	fi
done

mkdir -p "$output_directory"
entries_file="$work_directory/entries.jsonl"
while IFS= read -r tag; do
	[[ "$tag" =~ $tag_pattern ]] || {
		printf 'asset directory is not a version tag: %s\n' "$tag" >&2
		exit 1
	}
	archive_name="starport-docs-$tag.tar.gz"
	archive="$assets_directory/$tag/$archive_name"
	checksums="$assets_directory/$tag/checksums.txt"
	if [ ! -f "$checksums" ]; then
		printf 'release %s has no checksums.txt\n' "$tag" >&2
		exit 1
	fi
	expected="$(awk -v name="$archive_name" '$2 == name {print $1}' "$checksums")"
	actual="$(sha256 "$archive")"
	if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then
		printf 'checksum of %s does not match checksums.txt\n' "$archive_name" >&2
		exit 1
	fi

	# The archive holds one directory with plain files only.
	archive_root="starport-docs-$tag"
	entries="$work_directory/entries"
	tar -tzf "$archive" >"$entries"
	if grep -vE "^${archive_root//./\\.}(/|$)" "$entries" >/dev/null; then
		printf '%s has an entry outside %s/\n' "$archive_name" "$archive_root" >&2
		exit 1
	fi
	if grep -E '(^|/)\.\.(/|$)' "$entries" >/dev/null; then
		printf '%s has a parent directory entry\n' "$archive_name" >&2
		exit 1
	fi
	tar -tvzf "$archive" | cut -c1 >"$entries"
	if grep -vE '^[-d]$' "$entries" >/dev/null; then
		printf '%s has an entry that is not a file or a directory\n' "$archive_name" >&2
		exit 1
	fi
	extract_directory="$work_directory/extract/$tag"
	mkdir -p "$extract_directory"
	tar -xzf "$archive" -C "$extract_directory"
	version_directory="$output_directory/$tag"
	mv "$extract_directory/$archive_root" "$version_directory"

	# Every file matches the embedded manifest, and the manifest names the
	# release.
	embedded="$version_directory/manifest.json"
	if [ ! -f "$embedded" ]; then
		printf '%s has no manifest.json\n' "$archive_name" >&2
		exit 1
	fi
	if [ "$(jq -r .starport_release "$embedded")" != "$tag" ]; then
		printf '%s names release %s\n' "$archive_name" "$(jq -r .starport_release "$embedded")" >&2
		exit 1
	fi
	listed="$work_directory/listed"
	present="$work_directory/present"
	jq -r '.files | keys[]' "$embedded" | LC_ALL=C sort >"$listed"
	(cd "$version_directory" && find . -type f ! -path ./manifest.json | sed 's|^\./||' | LC_ALL=C sort) >"$present"
	if ! diff -u "$listed" "$present" >&2; then
		printf '%s files differ from its manifest\n' "$archive_name" >&2
		exit 1
	fi
	digests="$work_directory/digests"
	jq -r '.files | to_entries[] | "\(.value)  \(.key)"' "$embedded" >"$digests"
	if ! (cd "$version_directory" && sha256_check "$digests"); then
		printf '%s has a file that does not match its manifest digest\n' "$archive_name" >&2
		exit 1
	fi

	jq -c --arg tag "$tag" --arg archive "$archive_name" --arg digest "$actual" \
		'{tag: $tag, path: ($tag + "/"), archive: $archive, archive_sha256: $digest, manifest: .}' \
		"$embedded" >>"$entries_file"
done <"$tags_file"

cp -R "$output_directory/$latest_tag" "$output_directory/latest"
: >"$output_directory/.nojekyll"
jq -s --arg latest "$latest_tag" '{latest: $latest, versions: .}' "$entries_file" \
	>"$output_directory/manifest.json"

{
	cat <<EOF
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="0; url=latest/">
<link rel="canonical" href="latest/">
<title>Starport documentation</title>
</head>
<body>
<main>
<h1>Starport documentation</h1>
<p><a href="latest/">Open the latest documentation ($latest_tag)</a>.</p>
<h2>Versions</h2>
<ul>
EOF
	while IFS= read -r tag; do
		printf '<li><a href="%s/">%s</a></li>\n' "$tag" "$tag"
	done <"$tags_file"
	cat <<EOF
</ul>
</main>
</body>
</html>
EOF
} >"$output_directory/index.html"

printf 'Assembled %s. Versions: %s. Latest: %s.\n' \
	"$output_directory" "$(paste -sd ' ' "$tags_file")" "$latest_tag"
