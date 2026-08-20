#!/usr/bin/env bash

set -uo pipefail

ROOT="${STARPORT_DEPENDENCY_DIRECTION_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
MODULE="github.com/agentstation/starport"
RESULTS="$(mktemp -d "${TMPDIR:-/tmp}/starport-dependency-direction.XXXXXX")"
trap 'rm -rf "$RESULTS"' EXIT

passed=0
failed=0

run_condition() {
	local id="$1"
	local description="$2"
	local check="$3"
	shift 3
	local output="$RESULTS/$id.log"

	if "$check" "$@" >"$output" 2>&1; then
		printf '%s PASS: %s\n' "$id" "$description"
		passed=$((passed + 1))
		return
	fi

	printf '%s FAIL: %s\n' "$id" "$description"
	sed 's/^/  /' "$output"
	failed=$((failed + 1))
}

check_import_absent() {
	local importer="$1"
	local forbidden="$2"
	local imports

	imports="$(cd "$ROOT" && go list -e -f '{{range .Imports}}{{println .}}{{end}}' "./$importer")" || return 1
	if grep -Fxq "$forbidden" <<<"$imports"; then
		printf '%s imports forbidden package %s\n' "$importer" "$forbidden"
		return 1
	fi
}

check_proxy_field() {
	local field="$1"
	local field_type="$2"
	local display_type="${3:-$field_type}"
	local source_root="$ROOT/internal/proxy"
	local pattern="^[[:space:]]*${field}[[:space:]]+${field_type}([[:space:]]|$)"

	if ! grep -R -q -E --include='*.go' --exclude='*_test.go' "$pattern" "$source_root"; then
		printf 'proxy Config must declare %s with contract type %s\n' "$field" "$display_type"
		return 1
	fi
}

run_condition SP-D01 "proxy does not import the concrete cache adapter" \
	check_import_absent "internal/proxy" "$MODULE/internal/cache"
run_condition SP-D02 "proxy does not import the concrete provider registry" \
	check_import_absent "internal/proxy" "$MODULE/internal/registry"
run_condition SP-D03 "proxy exposes the cache behavior contract" \
	check_proxy_field "CacheManager" "CacheManager"
run_condition SP-D04 "proxy exposes the provider leasing contract" \
	check_proxy_field "Registry" "connectors\\.LeasingRegistry" "connectors.LeasingRegistry"
run_condition SP-D05 "app does not import Starmap source selection" \
	check_import_absent "internal/app" "github.com/agentstation/starmap/pkg/sources"
run_condition SP-D06 "app does not import Starmap sync options" \
	check_import_absent "internal/app" "github.com/agentstation/starmap/pkg/sync"

printf 'Summary: %d passed, %d failed\n' "$passed" "$failed"
if ((failed != 0)); then
	exit 1
fi
