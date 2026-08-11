#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
	echo "usage: measure-accepted.sh STARMAP_REPOSITORY" >&2
	exit 2
fi

root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
starmap_repository="$(cd "$1" && pwd)"
raw="$root/raw-starmap/accepted"
binary="$(mktemp)"
trap 'rm -f "$binary"' EXIT
mkdir -p "$raw"

(
	cd "$starmap_repository"
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
		-trimpath -ldflags='-s -w' -o "$binary" ./cmd/starmap
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go list -deps \
		-f '{{.ImportPath}}' ./cmd/starmap | LC_ALL=C sort -u >"$raw/packages.txt"
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go list -deps \
		-f '{{with .Module}}{{.Path}}@{{.Version}}{{end}}' ./cmd/starmap |
		sed '/^$/d' | LC_ALL=C sort -u >"$raw/modules.txt"
)

go version -m "$binary" >"$raw/build-info.txt"
binary_bytes="$(wc -c <"$binary" | tr -d ' ')"
baseline_bytes="$(awk -F '\t' '$1 == "baseline" { print $2 }' "$root/raw-starmap/summary.tsv")"
delta_bytes="$((binary_bytes - baseline_bytes))"
delta_percent="$(awk -v delta="$delta_bytes" -v base="$baseline_bytes" 'BEGIN { printf "%.4f", delta * 100 / base }')"
module_count="$(wc -l <"$raw/modules.txt" | tr -d ' ')"
package_count="$(wc -l <"$raw/packages.txt" | tr -d ' ')"
printf 'adapter\tbinary_bytes\tdelta_bytes\tdelta_percent\tmodules\tpackages\n' >"$raw/summary.tsv"
printf 'accepted\t%s\t%s\t%s\t%s\t%s\n' \
	"$binary_bytes" "$delta_bytes" "$delta_percent" "$module_count" "$package_count" \
	>>"$raw/summary.tsv"
