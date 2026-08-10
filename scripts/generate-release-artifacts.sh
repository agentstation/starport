#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
output_argument="${1:-build/release-artifacts}"

if (($# > 1)); then
	printf 'usage: %s [build/release-artifacts]\n' "$0" >&2
	exit 1
fi

case "$output_argument" in
	build/release-artifacts | "$repository_root/build/release-artifacts")
		output_directory="$repository_root/build/release-artifacts"
		;;
	*)
		printf 'release artifact output must be %s/build/release-artifacts\n' "$repository_root" >&2
		exit 1
		;;
esac

mkdir -p "$repository_root/build"
staging_directory="$(mktemp -d "$repository_root/build/release-artifacts.XXXXXX")"
trap 'rm -rf "$staging_directory"' EXIT

mkdir -p "$staging_directory/completions" "$staging_directory/manpages"
generator="$staging_directory/starport"
go build -trimpath -o "$generator" ./cmd/starport

"$generator" completion bash >"$staging_directory/completions/starport.bash"
"$generator" completion zsh >"$staging_directory/completions/starport.zsh"
"$generator" completion fish >"$staging_directory/completions/starport.fish"
"$generator" completion pwsh >"$staging_directory/completions/starport.ps1"
"$generator" man >"$staging_directory/manpages/starport.1"
rm -f "$generator"

for artifact in \
	"$staging_directory/completions/starport.bash" \
	"$staging_directory/completions/starport.zsh" \
	"$staging_directory/completions/starport.fish" \
	"$staging_directory/completions/starport.ps1" \
	"$staging_directory/manpages/starport.1"; do
	if [[ ! -s "$artifact" ]]; then
		printf 'generated release artifact is empty: %s\n' "$artifact" >&2
		exit 1
	fi
done

if ! grep -q '^\.TH starport 1' "$staging_directory/manpages/starport.1"; then
	printf 'generated manual is not a section-1 starport manual\n' >&2
	exit 1
fi

rm -rf "$output_directory"
mv "$staging_directory" "$output_directory"
trap - EXIT

printf 'generated shell completions and starport(1) under %s\n' "$output_directory"
