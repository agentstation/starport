#!/usr/bin/env bash

set -euo pipefail

distribution_directory="${1:-dist}"
metadata_file="$distribution_directory/metadata.json"
# This value is unset unless an approved release policy supplies a hard budget.
approved_maximum_binary_size_bytes="${APPROVED_MAX_RELEASE_BINARY_SIZE_BYTES:-}"

if [ -n "$approved_maximum_binary_size_bytes" ] &&
	! [[ "$approved_maximum_binary_size_bytes" =~ ^[0-9]+$ ]]; then
	printf 'approved maximum release binary size must be an integer number of bytes\n' >&2
	exit 1
fi

if [ ! -f "$metadata_file" ]; then
	printf 'release metadata is missing: %s\n' "$metadata_file" >&2
	exit 1
fi

expected_version="${2:-$(jq -r .version "$metadata_file")}"
expected_commit="$(jq -r .commit "$metadata_file")"
expected_targets="$({
	printf '%s\n' \
		'darwin/amd64' \
		'darwin/arm64' \
		'linux/amd64' \
		'linux/arm64' \
		'windows/amd64' \
		'windows/arm64'
})"
actual_targets="$(mktemp "${TMPDIR:-/tmp}/starport-release-targets.XXXXXX")"
trap 'rm -f "$actual_targets"' EXIT

verified=0
native_verified=0
host_os="$(go env GOOS)"
host_arch="$(go env GOARCH)"

while IFS= read -r binary; do
	build_info="$(go version -m "$binary" 2>/dev/null || true)"
	if [[ "$build_info" != *$'\tpath\tgithub.com/agentstation/starport/cmd/starport'* ]]; then
		continue
	fi

	if ! grep -Eq $'^[[:space:]]*build[[:space:]]+CGO_ENABLED=0$' <<<"$build_info"; then
		printf 'release binary is not recorded as a cgo-disabled build: %s\n' \
			"$binary" >&2
		exit 1
	fi
	if ! grep -Eq $'^[[:space:]]*build[[:space:]]+vcs.modified=false$' <<<"$build_info"; then
		printf 'release binary was built from a modified worktree: %s\n' "$binary" >&2
		exit 1
	fi
	if ! grep -Fq $'build\tvcs.revision='"$expected_commit" <<<"$build_info"; then
		printf 'release binary commit does not match metadata: %s\n' "$binary" >&2
		exit 1
	fi

	goos="$(awk '$1 == "build" && $2 ~ /^GOOS=/ { sub(/^GOOS=/, "", $2); print $2 }' <<<"$build_info")"
	goarch="$(awk '$1 == "build" && $2 ~ /^GOARCH=/ { sub(/^GOARCH=/, "", $2); print $2 }' <<<"$build_info")"
	printf '%s/%s\n' "$goos" "$goarch" >>"$actual_targets"

	size="$(wc -c <"$binary" | tr -d '[:space:]')"
	printf 'release binary size: %s (%s bytes)\n' "$binary" "$size"
	if [ -n "$approved_maximum_binary_size_bytes" ] &&
		[ "$size" -gt "$approved_maximum_binary_size_bytes" ]; then
		printf 'release binary exceeds the %s-byte budget: %s (%s bytes)\n' \
			"$approved_maximum_binary_size_bytes" "$binary" "$size" >&2
		exit 1
	fi

	case "$goos" in
	darwin)
		if command -v otool >/dev/null 2>&1; then
			unexpected="$(
				otool -L "$binary" |
					tail -n +2 |
					awk '{print $1}' |
					grep -Ev '^(/usr/lib/|/System/Library/)' || true
			)"
			if [ -n "$unexpected" ]; then
				printf 'Darwin binary has a non-system dynamic dependency: %s\n%s\n' \
					"$binary" "$unexpected" >&2
				exit 1
			fi
		fi
		;;
	linux)
		if command -v readelf >/dev/null 2>&1; then
			if readelf -lW "$binary" | grep -q INTERP ||
				readelf -dW "$binary" 2>&1 | grep -q '(NEEDED)'; then
				printf 'Linux release binary is dynamically linked: %s\n' "$binary" >&2
				exit 1
			fi
		elif ! file "$binary" | grep -q 'statically linked'; then
			printf 'Linux release binary is not reported as statically linked: %s\n' \
				"$binary" >&2
			exit 1
		fi
		;;
	windows)
		if objdump -p "$binary" |
			grep -Eqi 'DLL Name:.*(msvcrt|ucrtbase|vcruntime|libgcc|libstdc\+\+)'; then
			printf 'Windows release binary imports a C or C++ runtime: %s\n' "$binary" >&2
			exit 1
		fi
		;;
	esac

	if [ "$goos" = "$host_os" ] && [ "$goarch" = "$host_arch" ]; then
		version_output="$("$binary" --version)"
		if [ "$version_output" != "starport version $expected_version" ]; then
			printf 'native binary version mismatch: got %q, want %q\n' \
				"$version_output" "starport version $expected_version" >&2
			exit 1
		fi
		native_verified=$((native_verified + 1))
	fi

	verified=$((verified + 1))
done < <(find "$distribution_directory" -type f \( -name starport -o -name starport.exe \) | LC_ALL=C sort)

if [ "$verified" -ne 6 ]; then
	printf 'verified %s release binaries; want exactly 6\n' "$verified" >&2
	exit 1
fi
if [ "$native_verified" -ne 1 ]; then
	printf 'verified %s native release binaries; want exactly 1\n' "$native_verified" >&2
	exit 1
fi
if ! diff -u <(printf '%s\n' "$expected_targets") <(LC_ALL=C sort -u "$actual_targets"); then
	printf 'release target matrix does not match the supported targets\n' >&2
	exit 1
fi

printf 'PASS 6 version-exact cgo-disabled release binaries for the supported target matrix\n'
