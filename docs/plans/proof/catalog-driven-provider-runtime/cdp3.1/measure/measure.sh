#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
raw="$root/raw"
binary_dir="$(mktemp -d)"
trap 'rm -rf "$binary_dir"' EXIT
mkdir -p "$raw"

printf 'adapter\tbinary_bytes\tmodules\tpackages\n' >"$raw/summary.tsv"

for adapter in baseline gcp azure aws vault openbao all; do
	adapter_dir="$raw/$adapter"
	mkdir -p "$adapter_dir"
	binary="$binary_dir/$adapter"
	if [[ "$adapter" == baseline ]]; then
		CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
			-trimpath -ldflags='-s -w' -o "$binary" "$root"
		go list -deps -f '{{.ImportPath}}' "$root" |
			LC_ALL=C sort -u >"$adapter_dir/packages.txt"
		go list -deps -f '{{with .Module}}{{.Path}}@{{.Version}}{{end}}' "$root" |
			sed '/^$/d' | LC_ALL=C sort -u >"$adapter_dir/modules.txt"
	else
		CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
			-trimpath -ldflags='-s -w' -tags="$adapter" \
			-o "$binary" "$root"
		go list -tags="$adapter" -deps -f '{{.ImportPath}}' "$root" |
			LC_ALL=C sort -u >"$adapter_dir/packages.txt"
		go list -tags="$adapter" -deps \
			-f '{{with .Module}}{{.Path}}@{{.Version}}{{end}}' "$root" |
			sed '/^$/d' | LC_ALL=C sort -u >"$adapter_dir/modules.txt"
	fi
	go version -m "$binary" >"$adapter_dir/build-info.txt"

	binary_bytes="$(wc -c <"$binary" | tr -d ' ')"
	module_count="$(wc -l <"$adapter_dir/modules.txt" | tr -d ' ')"
	package_count="$(wc -l <"$adapter_dir/packages.txt" | tr -d ' ')"
	printf '%s\t%s\t%s\t%s\n' \
		"$adapter" "$binary_bytes" "$module_count" "$package_count" \
		>>"$raw/summary.tsv"
done

go env GOVERSION GOOS GOARCH >"$raw/toolchain.txt"
printf 'CGO_ENABLED=0\nGOOS=linux\nGOARCH=amd64\nflags=-trimpath -ldflags=-s -w\n' \
	>"$raw/build-flags.txt"
