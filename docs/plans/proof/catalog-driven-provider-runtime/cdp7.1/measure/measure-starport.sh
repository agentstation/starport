#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
	echo "usage: measure-starport.sh STARPORT_REPOSITORY" >&2
	exit 2
fi

root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
starport_repository="$(cd "$1" && pwd)"
raw="$root/raw"
baseline_commit="613e4fa3a9e912faa064a488d983aaf1f380cee4"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

mkdir -p "$raw"
printf 'adapter\tbinary_bytes\tdelta_bytes\tdelta_percent\tmodules\tpackages\n' >"$raw/summary.tsv"

baseline_bytes=0
for adapter in baseline gcp azure aws vault openbao accepted; do
	adapter_dir="$raw/$adapter"
	binary="$work/$adapter"
	source="$work/$adapter-source"
	mkdir -p "$adapter_dir" "$source"
	git -C "$starport_repository" archive "$baseline_commit" | tar -x -C "$source"

	build_tags=()
	case "$adapter" in
		baseline) ;;
		gcp) build_tags=(-tags=measure_gcp); cp "$root/measure_gcp.go" "$source/cmd/starport/" ;;
		azure) build_tags=(-tags=measure_azure); cp "$root/measure_azure.go" "$source/cmd/starport/" ;;
		aws) build_tags=(-tags=measure_aws); cp "$root/measure_aws.go" "$source/cmd/starport/" ;;
		vault) build_tags=(-tags=measure_vault); cp "$root/measure_vault.go" "$source/cmd/starport/" ;;
		openbao) build_tags=(-tags=measure_openbao); cp "$root/measure_openbao.go" "$source/cmd/starport/" ;;
		accepted)
			build_tags=("-tags=measure_gcp,measure_azure,measure_aws,measure_vault,measure_openbao")
			cp "$root"/measure_*.go "$source/cmd/starport/"
			;;
	esac

	(
		cd "$source"
		if [[ ${#build_tags[@]} -eq 0 ]]; then
			CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
				-trimpath -ldflags='-s -w' -o "$binary" ./cmd/starport
			CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go list \
				-deps -f '{{.ImportPath}}' ./cmd/starport |
				LC_ALL=C sort -u >"$adapter_dir/packages.txt"
			CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go list \
				-deps -f '{{with .Module}}{{.Path}}@{{.Version}}{{end}}' ./cmd/starport |
				sed '/^$/d' | LC_ALL=C sort -u >"$adapter_dir/modules.txt"
		else
			CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
				-trimpath -ldflags='-s -w' "${build_tags[@]}" \
				-o "$binary" ./cmd/starport
			CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go list "${build_tags[@]}" \
				-deps -f '{{.ImportPath}}' ./cmd/starport |
				LC_ALL=C sort -u >"$adapter_dir/packages.txt"
			CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go list "${build_tags[@]}" \
				-deps -f '{{with .Module}}{{.Path}}@{{.Version}}{{end}}' ./cmd/starport |
				sed '/^$/d' | LC_ALL=C sort -u >"$adapter_dir/modules.txt"
		fi
	)
	go version -m "$binary" >"$adapter_dir/build-info.txt"
	binary_bytes="$(wc -c <"$binary" | tr -d ' ')"
	if [[ "$adapter" == baseline ]]; then
		baseline_bytes="$binary_bytes"
	fi
	delta_bytes="$((binary_bytes - baseline_bytes))"
	delta_percent="$(awk -v delta="$delta_bytes" -v base="$baseline_bytes" 'BEGIN { printf "%.4f", delta * 100 / base }')"
	module_count="$(wc -l <"$adapter_dir/modules.txt" | tr -d ' ')"
	package_count="$(wc -l <"$adapter_dir/packages.txt" | tr -d ' ')"
	printf '%s\t%s\t%s\t%s\t%s\t%s\n' \
		"$adapter" "$binary_bytes" "$delta_bytes" "$delta_percent" \
		"$module_count" "$package_count" >>"$raw/summary.tsv"
done

go env GOVERSION >"$raw/toolchain.txt"
printf 'baseline_commit=%s\nCGO_ENABLED=0\nGOOS=linux\nGOARCH=amd64\nflags=-trimpath -ldflags=-s -w\n' \
	"$baseline_commit" >"$raw/build-flags.txt"
