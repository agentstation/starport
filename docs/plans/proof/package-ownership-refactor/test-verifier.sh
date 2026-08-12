#!/bin/sh

set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
output_file=$(mktemp "${TMPDIR:-/tmp}/por-verifier.XXXXXX")
trap 'rm -f "$output_file"' EXIT HUP INT TERM
starport_root=${STARPORT_ROOT:-$(CDPATH='' cd -- "$script_dir/../../../.." && pwd)}
starmap_root=${STARMAP_ROOT:-$(dirname "$starport_root")/starmap}

set +e
STARPORT_ROOT=$starport_root \
STARMAP_ROOT=$starmap_root \
    bash "$script_dir/verify.sh" >"$output_file" 2>&1
status=$?
set -e

result_count=$(grep -Ec '^(PASS|FAIL) POR-V[0-9][0-9] ' "$output_file" || true)
unique_count=$(sed -E -n 's/^(PASS|FAIL) (POR-V[0-9][0-9]).*/\2/p' "$output_file" | sort -u | wc -l | tr -d ' ')
passed=$(sed -n 's/^Summary: \([0-9][0-9]*\) passed, \([0-9][0-9]*\) failed$/\1/p' "$output_file")
failed=$(sed -n 's/^Summary: \([0-9][0-9]*\) passed, \([0-9][0-9]*\) failed$/\2/p' "$output_file")

test "$result_count" -eq 9
test "$unique_count" -eq 9
test -n "$passed"
test -n "$failed"
test $((passed + failed)) -eq 9

if [ "$failed" -eq 0 ]; then
    test "$status" -eq 0
else
    test "$status" -eq 1
fi

if STARPORT_ROOT="$script_dir/not-a-repository" \
    STARMAP_ROOT="$script_dir/not-a-repository" \
    bash "$script_dir/verify.sh" >/dev/null 2>&1; then
    printf 'FAIL campaign verifier accepted invalid repository roots\n' >&2
    exit 1
fi

printf 'PASS package ownership campaign verifier contract (%s passed, %s failed)\n' "$passed" "$failed"
