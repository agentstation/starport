#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
fixtures="$root/scripts/testdata/doc-links"

bash "$root/scripts/verify-doc-links.sh" \
  "$fixtures/valid.txt" >/dev/null

if bash "$root/scripts/verify-doc-links.sh" \
  "$fixtures/broken.txt" >/dev/null 2>&1; then
  printf 'FAIL documentation link verifier accepted a missing target\n' >&2
  exit 1
fi

printf 'PASS documentation link verifier edge cases\n'
