#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
	echo "usage: measure-starmap.sh STARMAP_REPOSITORY" >&2
	exit 2
fi

root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
starmap_repository="$(cd "$1" && pwd)"
raw="$root/raw-starmap"
baseline_commit="54bd8de9ea9f6c26188bf2ebb54dc5f647758ef9"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

mkdir -p "$raw"

printf 'adapter\tbinary_bytes\tdelta_bytes\tdelta_percent\tmodules\tpackages\n' >"$raw/summary.tsv"

baseline_bytes=0
for adapter in baseline gcp azure aws vault openbao accepted all; do
	adapter_dir="$raw/$adapter"
	binary="$work/$adapter"
	source="$work/$adapter-source"
	mkdir -p "$adapter_dir"
	mkdir -p "$source"
	git -C "$starmap_repository" archive "$baseline_commit" | tar -x -C "$source"
	case "$adapter" in
		baseline) build_tags='' ;;
		gcp) build_tags='measure_gcp'; cp "$root/starmap-tags/measure_gcp.go" "$source/cmd/starmap/" ;;
		azure) build_tags='measure_azure'; cp "$root/starmap-tags/measure_azure.go" "$source/cmd/starmap/" ;;
		aws) build_tags='measure_aws'; cp "$root/starmap-tags/measure_aws.go" "$source/cmd/starmap/" ;;
		vault) build_tags='measure_vault'; cp "$root/starmap-tags/measure_vault.go" "$source/cmd/starmap/" ;;
		openbao) build_tags='measure_openbao'; cp "$root/starmap-tags/measure_openbao.go" "$source/cmd/starmap/" ;;
		accepted)
			build_tags='measure_gcp,measure_azure,measure_aws,measure_vault'
			cp "$root"/starmap-tags/measure_{gcp,azure,aws,vault}.go "$source/cmd/starmap/"
			;;
		all)
			build_tags='measure_gcp,measure_azure,measure_aws,measure_vault,measure_openbao'
			cp "$root"/starmap-tags/*.go "$source/cmd/starmap/"
			;;
	esac
	(
		cd "$source"
		case "$adapter" in
			gcp)
				go get cloud.google.com/go/secretmanager@v1.21.0
				;;
			azure)
				go get github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets@v1.5.0
				;;
			aws)
				go get github.com/aws/aws-sdk-go-v2/service/secretsmanager@v1.44.5
				;;
			vault)
				go get github.com/hashicorp/vault/api@v1.23.0
				;;
			openbao)
				go get github.com/openbao/openbao/api/v2@v2.6.0
				;;
			accepted)
				go get \
					cloud.google.com/go/secretmanager@v1.21.0 \
					github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets@v1.5.0 \
					github.com/aws/aws-sdk-go-v2/service/secretsmanager@v1.44.5 \
					github.com/hashicorp/vault/api@v1.23.0
				;;
			all)
				go get \
					cloud.google.com/go/secretmanager@v1.21.0 \
					github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets@v1.5.0 \
					github.com/aws/aws-sdk-go-v2/service/secretsmanager@v1.44.5 \
					github.com/hashicorp/vault/api@v1.23.0 \
					github.com/openbao/openbao/api/v2@v2.6.0
				;;
		esac
		go mod tidy
	)
	tag_arg=''
	if [[ -n "$build_tags" ]]; then
		tag_arg="-tags=$build_tags"
	fi
	(
		cd "$source"
		CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
			-trimpath -ldflags='-s -w' ${tag_arg:+"$tag_arg"} \
			-o "$binary" ./cmd/starmap
		CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go list ${tag_arg:+"$tag_arg"} \
			-deps -f '{{.ImportPath}}' ./cmd/starmap |
			LC_ALL=C sort -u >"$adapter_dir/packages.txt"
		CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go list ${tag_arg:+"$tag_arg"} \
			-deps -f '{{with .Module}}{{.Path}}@{{.Version}}{{end}}' ./cmd/starmap |
			sed '/^$/d' | LC_ALL=C sort -u >"$adapter_dir/modules.txt"
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
