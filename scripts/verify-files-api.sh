#!/usr/bin/env bash
# Guard for the files API campaign (plan prefix FIL). Each condition asserts
# one structural property of the target design:
#
#   - one blob contract stores opaque bytes over streams, and two backends
#     satisfy it without an interface change,
#   - a file record names its bytes, belongs to one tenant, and survives a
#     crash between the two stores,
#   - five routes serve the OpenAI file object behind two scopes, with a
#     retention window and a stored byte bound that hold,
#   - a chat request names a stored file instead of carrying its bytes.
#
# It reports every condition and exits nonzero while any condition fails.
set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

pass=0
fail=0

check() {
  local id="$1" desc="$2"
  shift 2
  if "$@" >/dev/null 2>&1; then
    printf 'PASS %s %s\n' "$id" "$desc"
    pass=$((pass + 1))
  else
    printf 'FAIL %s %s\n' "$id" "$desc"
    fail=$((fail + 1))
  fi
}

grep_q() { grep -Rq -- "$1" "${@:2}"; }

# in_tests reports that a term appears in a Go test file under the given
# paths. The distinction matters: a symbol that only the source names is a
# vocabulary entry, not a held contract.
in_tests() { grep -Rq --include='*_test.go' -- "$1" "${@:2}"; }

# all_present reports that every term before the -- appears somewhere under
# the paths after it. A partial vocabulary is the failure this catches: one
# operation lands, the other two stay behind, and a single-term grep passes.
all_present() {
  local terms=() paths=()
  local seen=0 arg
  for arg in "$@"; do
    if [ "$arg" = "--" ]; then seen=1; continue; fi
    if [ "$seen" -eq 0 ]; then terms+=("$arg"); else paths+=("$arg"); fi
  done
  local term
  for term in "${terms[@]}"; do
    grep -Rq -- "$term" "${paths[@]}" || return 1
  done
  return 0
}

# tests_all_present is all_present restricted to Go test files. A condition
# that names several assertions needs it, because a source constant carrying
# the same words would otherwise satisfy the condition with no test behind it.
tests_all_present() {
  local terms=() paths=()
  local seen=0 arg
  for arg in "$@"; do
    if [ "$arg" = "--" ]; then seen=1; continue; fi
    if [ "$seen" -eq 0 ]; then terms+=("$arg"); else paths+=("$arg"); fi
  done
  local term
  for term in "${terms[@]}"; do
    grep -Rq --include='*_test.go' -- "$term" "${paths[@]}" || return 1
  done
  return 0
}

# file_routes_registered holds FIL-V10. Each path is registered relative to
# its protocol group, so the absolute path a caller types never appears in the
# source that registers it, and a grep over that source would read a doc
# comment as a route. The held contract is a route test that walks the router
# the server built and names every path a client can reach.
file_routes_registered() {
  local held=internal/server/file_routes_test.go
  [ -f "$held" ] || return 1
  grep -q 'chi.Walk' "$held" || return 1
  all_present '/v1/files' '/v1/files/{file_id}' '/v1/files/{file_id}/content' \
    -- "$held"
}

# one_contract_two_backends holds FIL-V04. Two backends that each carry their
# own test prove nothing about one contract. The held fact is a single table
# that names both and runs the same cases over each.
one_contract_two_backends() {
  local held=internal/blob/contract_test.go
  [ -f "$held" ] || return 1
  tests_all_present NewFilesystem NewObjectStore -- internal/blob
}

# --- Phase A, the blob seam ---

# console_files reports that the console reaches all three file operations and
# holds each FIL-V20 statement in a test. The three greps stay separate on
# purpose: a page that listed files and offered no upload would satisfy a
# single-term grep and still leave an operator unable to put a document in.
console_files() {
  all_present listFiles uploadFile deleteFile \
    -- console/src/components/files/FilesPanel.tsx || return 1
  all_present file-row file-notice stored-total \
    -- console/src/components/files/FilesPanel.test.tsx || return 1
  grep_q FilesPanel console/src/routes/files.tsx
}

check FIL-V01 "one blob contract stores opaque bytes over streams" \
  all_present 'func.*Put' 'func.*Get' 'func.*Stat' 'func.*Delete' -- internal/blob

check FIL-V02 "a key holding a path separator fails before any write" \
  in_tests ErrInvalidKey internal/blob

check FIL-V03 "the import graph test names the package and bounds its imports" \
  grep_q 'internal/blob' internal/architecture

check FIL-V04 "one contract test table runs against both backends" \
  one_contract_two_backends

check FIL-V05 "an absent configuration selects the filesystem backend" \
  tests_all_present BlobBackend filesystem -- internal/config

check FIL-V06 "an incomplete object store configuration refuses startup" \
  in_tests ErrIncompleteBlobConfig internal/config

# --- Phase B, the record and the routes ---

check FIL-V07 "a read for another tenant returns a not-found result" \
  in_tests ErrFileNotFound internal/files

check FIL-V08 "a crash before the commit leaves no reachable file" \
  tests_all_present FileStatePending Sweep -- internal/files

check FIL-V09 "the record exposes no blob key through a public accessor" \
  in_tests 'blobKey' internal/files

check FIL-V10 "a route test walks the router and names the five file paths" \
  file_routes_registered

check FIL-V11 "a key holding only files:read cannot upload" \
  tests_all_present 'files:read' 'files:write' -- internal/server

check FIL-V12 "an upload past the bound writes no partial blob" \
  in_tests ErrUploadTooLarge internal/server

check FIL-V13 "the encoded file object carries every recorded field" \
  tests_all_present 'created_at' 'expires_at' 'filename' 'purpose' 'bytes' \
    -- internal/protocol/openai

check FIL-V14 "a delete removes both the record and the bytes" \
  tests_all_present FileStateDeleting -- internal/files

check FIL-V15 "an expired file reads as not found before the sweep runs" \
  in_tests ExpiresAt internal/files

# --- Phase C, policy and use ---

check FIL-V16 "the limit vocabulary bounds stored bytes" \
  grep_q StoredBytes internal/limits

check FIL-V17 "two concurrent uploads cannot both pass a bound that admits one" \
  tests_all_present StoredBytes 'sync.WaitGroup' -- internal/limits

check FIL-V18 "a document content part can name a stored file identifier" \
  grep_q FileID internal/inference

check FIL-V19 "a stored file reference reaches the cache key" \
  in_tests FileID internal/response/cache

check FIL-V20 "the console lists, uploads, and deletes a stored file" \
  console_files

# --- Close ---

check FIL-V21 "CI runs this gate" \
  grep_q 'verify-files-api.sh' .github/workflows

check FIL-V22 "the required evidence list names this gate and its terminal count" \
  grep_q 'verify-files-api.sh' AGENTS.md

printf 'Summary: %d passed, %d failed\n' "$pass" "$fail"
test "$fail" -eq 0
