#!/usr/bin/env bash

# test-docs-pages-assembler.sh builds two docs release archives from the
# local console build and runs assemble-docs-pages.sh on them offline. It
# checks the layout, the redirect, .nojekyll, and the root manifest, and it
# checks that the assembler refuses a bad archive. Run pnpm -C console build
# or make build first.

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
docs="$root/internal/console/dist/docs"
assembler="$root/scripts/assemble-docs-pages.sh"

if [ ! -f "$docs/manifest.json" ]; then
	printf 'FAIL %s has no build; run pnpm -C console build first\n' "$docs" >&2
	exit 1
fi

work="$(mktemp -d "${TMPDIR:-/tmp}/starport-docs-pages-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

fail() {
	printf 'FAIL docs Pages assembler: %s\n' "$1" >&2
	exit 1
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	else
		shasum -a 256 "$1" | awk '{print $1}'
	fi
}

# fixture writes the release assets of one tag: the docs archive in the
# goreleaser layout and its checksums.txt. The embedded manifest names the
# tag. Its file map stays the same because it does not list itself.
fixture() {
	local assets="$1" tag="$2"
	local staging="$work/staging/$tag"
	mkdir -p "$staging" "$assets/$tag"
	cp -R "$docs" "$staging/starport-docs-$tag"
	jq --arg tag "$tag" '.starport_release = $tag' "$docs/manifest.json" \
		>"$staging/starport-docs-$tag/manifest.json"
	COPYFILE_DISABLE=1 tar -czf "$assets/$tag/starport-docs-$tag.tar.gz" \
		-C "$staging" "starport-docs-$tag"
	printf '%s  %s\n' "$(sha256 "$assets/$tag/starport-docs-$tag.tar.gz")" \
		"starport-docs-$tag.tar.gz" >"$assets/$tag/checksums.txt"
}

assets="$work/assets"
fixture "$assets" v9.9.8
fixture "$assets" v9.9.10

# Deploy v9.9.10 and keep v9.9.8 as latest, the rollback case.
out="$work/site"
bash "$assembler" --assets-dir "$assets" v9.9.10 v9.9.8 "$out" >/dev/null

for tag in v9.9.8 v9.9.10; do
	[ -f "$out/$tag/index.html" ] || fail "$tag/index.html is missing"
	diff -r "$work/staging/$tag/starport-docs-$tag" "$out/$tag" >/dev/null ||
		fail "$tag/ differs from its archive"
done
diff -r "$out/v9.9.8" "$out/latest" >/dev/null || fail "latest/ is not the latest tag"
[ -f "$out/.nojekyll" ] || fail ".nojekyll is missing"

grep -F '<meta http-equiv="refresh" content="0; url=latest/">' "$out/index.html" >/dev/null ||
	fail "index.html does not redirect to latest/"
grep -F '<a href="v9.9.10/">v9.9.10</a>' "$out/index.html" >/dev/null ||
	fail "index.html does not list v9.9.10"
grep -F '<a href="v9.9.8/">v9.9.8</a>' "$out/index.html" >/dev/null ||
	fail "index.html does not list v9.9.8"

manifest="$out/manifest.json"
[ "$(jq -r .latest "$manifest")" = v9.9.8 ] || fail "manifest latest is not v9.9.8"
[ "$(jq -c '[.versions[].tag]' "$manifest")" = '["v9.9.10","v9.9.8"]' ] ||
	fail "manifest versions are not newest first"
for tag in v9.9.8 v9.9.10; do
	entry="$(jq -c --arg tag "$tag" '.versions[] | select(.tag == $tag)' "$manifest")"
	[ "$(jq -r .path <<<"$entry")" = "$tag/" ] || fail "$tag path"
	[ "$(jq -r .archive <<<"$entry")" = "starport-docs-$tag.tar.gz" ] || fail "$tag archive name"
	[ "$(jq -r .archive_sha256 <<<"$entry")" = "$(sha256 "$assets/$tag/starport-docs-$tag.tar.gz")" ] ||
		fail "$tag archive digest"
	# The entry equals the embedded manifest of the release, so A29 can
	# compare the public site with the embedded build.
	jq -e --slurpfile embedded "$out/$tag/manifest.json" '.manifest == $embedded[0]' <<<"$entry" >/dev/null ||
		fail "$tag entry does not equal its embedded manifest"
	jq -e --slurpfile built "$docs/manifest.json" '.manifest.files == $built[0].files' <<<"$entry" >/dev/null ||
		fail "$tag file map does not equal the build"
done

# refuses runs the assembler on a copy of the assets after a change and
# expects it to fail.
refuses() {
	local reason="$1" deploy="$2" latest="$3" target="$4"
	if bash "$assembler" --assets-dir "$work/bad" "$deploy" "$latest" "$target" >/dev/null 2>&1; then
		fail "accepted $reason"
	fi
}

rm -rf "$work/bad" && cp -R "$assets" "$work/bad"
refuses "a tag that is not a version" "v9.9.10;" v9.9.8 "$work/out-tag"
refuses "a latest tag without an archive" v9.9.10 v9.9.7 "$work/out-latest"
refuses "an output directory that is not empty" v9.9.10 v9.9.8 "$out"

printf '0000000000000000000000000000000000000000000000000000000000000000  starport-docs-v9.9.8.tar.gz\n' \
	>"$work/bad/v9.9.8/checksums.txt"
refuses "an archive with a wrong checksum" v9.9.10 v9.9.8 "$work/out-checksum"

rm -rf "$work/bad" && cp -R "$assets" "$work/bad"
tampered="$work/staging/v9.9.8/starport-docs-v9.9.8"
printf 'changed\n' >>"$tampered/index.html"
COPYFILE_DISABLE=1 tar -czf "$work/bad/v9.9.8/starport-docs-v9.9.8.tar.gz" -C "$work/staging/v9.9.8" starport-docs-v9.9.8
printf '%s  %s\n' "$(sha256 "$work/bad/v9.9.8/starport-docs-v9.9.8.tar.gz")" starport-docs-v9.9.8.tar.gz \
	>"$work/bad/v9.9.8/checksums.txt"
refuses "a file that differs from the embedded manifest" v9.9.10 v9.9.8 "$work/out-file"

printf 'PASS docs Pages assembler layout, redirect, manifest, and refusals\n'
