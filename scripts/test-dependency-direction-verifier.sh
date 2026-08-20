#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERIFIER="$ROOT/scripts/verify-dependency-direction.sh"
FIXTURE="$(mktemp -d "${TMPDIR:-/tmp}/starport-dependency-verifier.XXXXXX")"
trap 'rm -rf "$FIXTURE"' EXIT

assert_complete_report() {
	local report="$1"
	local condition_count
	local total

	condition_count="$(grep -Ec '^SP-D[0-9]{2} (PASS|FAIL):' "$report")"
	if [[ "$condition_count" != "6" ]]; then
		printf 'verifier reported %s conditions; want 6\n' "$condition_count" >&2
		exit 1
	fi
	total="$(awk '/^Summary:/ {print $2 + $4}' "$report")"
	if [[ "$total" != "6" ]]; then
		printf 'verifier summary covers %s conditions; want 6\n' "$total" >&2
		exit 1
	fi
}

write_proxy() {
	local registry_type="${1:-connectors.LeasingRegistry}"
	local cache_type="${2:-CacheManager}"

	mkdir -p "$FIXTURE/internal/proxy"
	{
		printf '%s\n' 'package proxy'
		printf '%s\n' 'import "github.com/agentstation/starport/internal/providers/connectors"'
		printf '%s\n' 'type CacheManager interface{}'
		printf 'type Config struct {\n\tRegistry %s\n\tCacheManager %s\n}\n' "$registry_type" "$cache_type"
	} >"$FIXTURE/internal/proxy/proxy.go"
}

write_import() {
	local path="$1"
	local package_name="$2"
	local imported="${3:-}"

	mkdir -p "$FIXTURE/$path"
	{
		printf 'package %s\n' "$package_name"
		if [[ -n "$imported" ]]; then
			printf 'import _ "%s"\n' "$imported"
		fi
	} >"$FIXTURE/$path/package.go"
}

write_proxy_import() {
	local imported="$1"
	printf 'package proxy\nimport _ "%s"\n' "$imported" >"$FIXTURE/internal/proxy/mutation.go"
}

printf 'module github.com/agentstation/starport\n\ngo 1.25.0\n' >"$FIXTURE/go.mod"
write_import "internal/app" "app"
write_import "internal/cache" "cache"
write_import "internal/registry" "registry"
write_import "internal/providers/connectors" "connectors"
printf '%s\n' 'package connectors' 'type LeasingRegistry interface{}' >"$FIXTURE/internal/providers/connectors/package.go"
write_proxy

clean_report="$FIXTURE/clean.txt"
STARPORT_DEPENDENCY_DIRECTION_ROOT="$FIXTURE" bash "$VERIFIER" >"$clean_report"
assert_complete_report "$clean_report"
grep -Fq 'Summary: 6 passed, 0 failed' "$clean_report"

run_mutation() {
	local id="$1"
	local expected="$2"
	local report="$FIXTURE/$id.txt"
	shift 2

	"$@"
	if STARPORT_DEPENDENCY_DIRECTION_ROOT="$FIXTURE" bash "$VERIFIER" >"$report" 2>&1; then
		printf '%s mutation unexpectedly passed verification\n' "$id" >&2
		exit 1
	fi
	assert_complete_report "$report"
	grep -Fq "$id FAIL:" "$report" || {
		printf '%s mutation did not fail its condition\n' "$id" >&2
		exit 1
	}
	grep -Fq 'Summary: 5 passed, 1 failed' "$report" || {
		printf '%s mutation affected more than one condition\n' "$id" >&2
		exit 1
	}
	grep -Fq "$expected" "$report" || {
		printf '%s failure did not name %s\n' "$id" "$expected" >&2
		exit 1
	}
}

run_mutation SP-D01 'internal/cache' \
	write_proxy_import "github.com/agentstation/starport/internal/cache"
rm -f "$FIXTURE/internal/proxy/mutation.go"

run_mutation SP-D02 'internal/registry' \
	write_proxy_import "github.com/agentstation/starport/internal/registry"
rm -f "$FIXTURE/internal/proxy/mutation.go"

run_mutation SP-D03 'CacheManager' write_proxy "connectors.LeasingRegistry" "*ConcreteCache"
write_proxy

run_mutation SP-D04 'connectors.LeasingRegistry' write_proxy "*ConcreteRegistry" "CacheManager"
write_proxy

run_mutation SP-D05 'pkg/sources' \
	write_import "internal/app" "app" "github.com/agentstation/starmap/pkg/sources"
write_import "internal/app" "app"

run_mutation SP-D06 'pkg/sync' \
	write_import "internal/app" "app" "github.com/agentstation/starmap/pkg/sync"
write_import "internal/app" "app"

if grep -Eq 'make[[:space:]]+verify|scripts/verify-v1-architecture\.sh' "$VERIFIER"; then
	printf 'dependency verifier invokes a full repository gate\n' >&2
	exit 1
fi

printf 'Starport dependency direction verifier tests passed\n'
