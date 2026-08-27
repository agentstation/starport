#!/usr/bin/env bash
# Guard for the document parser campaign (plan prefix PLG). Each condition
# asserts one structural property of the target design:
#
#   - one plugin seam decodes the OpenRouter file-parser plugin, names the
#     engines this gateway actually runs, and refuses every other name,
#   - one extraction seam reads a text layer in process and reaches no
#     provider, so a document that carries text costs nothing,
#   - Starmap owns the recognition operation, its offerings, and its per-page
#     price, and Starport reads all three from one catalog snapshot,
#   - a recognized page is cached once, billed once, and reported once, and a
#     plugin never moves the chat route.
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
# engine name lands, the other stays behind, and a single-term grep passes.
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

# two_engines_no_local_table holds PLG-V02. Starport runs exactly two engines:
# one in process and one through a catalogued offering. A third name would be
# a local engine table, which invariant P1 gives to Starmap.
two_engines_no_local_table() {
  all_present ParserEngineNative ParserEngineRecognition \
    -- internal/protocol/openrouter || return 1
  # A vendor engine name Starport cannot route to must not appear as an
  # accepted value. Accepting one and serving another vendor is the unkept
  # promise invariant P2 forbids. Test files are excluded on purpose: a test
  # that drives the refusal has to name the vendor engine to refuse it.
  ! grep -Rq --include='*.go' --exclude='*_test.go' -- 'mistral-ocr' internal/
}

# extraction_is_local holds PLG-V05. The native engine is the reason a
# text-bearing document costs nothing. A single provider import would make
# that claim false without changing any test.
extraction_is_local() {
  [ -d internal/document ] || return 1
  grep_q 'internal/document' internal/architecture || return 1
  ! grep -Rq --include='*.go' -- 'internal/providers' internal/document
}

# recognition_priced_per_page holds PLG-V07. An offering that names the
# operation and carries no page price would bill a caller nothing for real
# provider work, so the projection has to carry the price beside the operation.
recognition_priced_per_page() {
  all_present PageInput 'documents-recognition' -- internal/catalog || return 1
  tests_all_present PageInput -- internal/catalog
}

# extraction_cached_once holds PLG-V12. Three parts make the key, and dropping
# any one of them is a real defect: no hash re-extracts identical bytes, no
# engine returns native text for a recognition request, and no generation
# serves text a catalog change has invalidated.
extraction_cached_once() {
  [ -d internal/document ] || return 1
  tests_all_present ContentHash Engine Generation -- internal/document || return 1
  # A tenant that did not pay for an extraction must not read it.
  tests_all_present AccountID -- internal/document
}

# native_page_is_free holds PLG-V14. A page a chat model read natively draws
# no separate charge. Asserting only the recognized case would pass a build
# that billed every page twice.
native_page_is_free() {
  tests_all_present RecognizedPages ExtractionCost -- internal/usage || return 1
  tests_all_present ParserEngineNative -- internal/usage internal/proxy
}

# refused_before_the_call holds PLG-V15. The bound has to refuse before the
# recognition request leaves, because a caller past its limit would otherwise
# pay a provider for work the gateway then discards.
refused_before_the_call() {
  tests_all_present ExtractionMillis -- internal/usage || return 1
  tests_all_present ErrSpendLimitExceeded -- internal/limits internal/proxy
}

# transforms_unchanged holds PLG-V17. Enforcing the plugins field must not
# move the transforms field, and it must not move the parity gate. The count
# is the guard: a new condition there would mean this campaign widened a
# surface decision PLG-D5 kept closed.
transforms_unchanged() {
  tests_all_present Transforms DocumentParser \
    -- internal/protocol/openrouter || return 1
  [ "$(grep -c '^check ORP-V' scripts/verify-openrouter-parity.sh)" = "16" ]
}

# console_shows_the_cost holds PLG-V18. A reader who sees an extraction but
# not its price cannot tell a free native read from a paid recognition pass,
# which is the one question the console exists to answer here.
console_shows_the_cost() {
  local panel=console/src/components/documents/DocumentsPanel.tsx
  [ -f "$panel" ] || return 1
  all_present engine pages -- "$panel" || return 1
  all_present document-engine document-pages document-cost document-cached \
    -- console/src/components/documents/DocumentsPanel.test.tsx
}

# --- Phase A, the plugin seam and in-process extraction ---

check PLG-V01 "the codec decodes the file-parser plugin into a typed option" \
  all_present 'file-parser' ParserEngine -- internal/protocol/openrouter

check PLG-V02 "the engine vocabulary names the two engines this gateway runs" \
  two_engines_no_local_table

check PLG-V03 "an unknown engine and an unenforced plugin both draw typed refusals" \
  tests_all_present ErrUnknownParserEngine ErrUnenforcedPlugin \
    -- internal/protocol/openrouter

check PLG-V04 "in-process extraction reports text, a page count, and a scanned verdict" \
  tests_all_present Pages Scanned ErrPageBudgetExceeded -- internal/document

check PLG-V05 "the extraction package reaches no provider and the graph test names it" \
  extraction_is_local

# --- Phase B, the catalogued recognition path and its cache ---

check PLG-V06 "the catalog projects the recognition operation and the named set holds it" \
  all_present 'documents-recognition' -- internal/catalog internal/routing

check PLG-V07 "every recognition offering carries a per-page price" \
  recognition_priced_per_page

check PLG-V08 "a recognition offering with no page price fails projection" \
  tests_all_present ErrMissingPagePrice -- internal/catalog

check PLG-V09 "a scanned document reaches a recognition offering before the chat model" \
  tests_all_present OperationDocumentsRecognition -- internal/routing internal/proxy

check PLG-V10 "the plugin never moves the chat route" \
  tests_all_present DocumentParser -- internal/routing

check PLG-V11 "one failed page fails the whole turn with a typed error" \
  tests_all_present ErrRecognitionFailed -- internal/document internal/proxy

check PLG-V12 "identical bytes extract once per account, engine, and generation" \
  extraction_cached_once

check PLG-V13 "a cache hit costs no page and the response reports the hit" \
  tests_all_present ExtractionCached -- internal/document internal/proxy

# --- Phase C, accounting, parity, and the console ---

check PLG-V14 "a recognized page draws a cost and a natively read page draws none" \
  native_page_is_free

check PLG-V15 "the spend bound refuses before the recognition call" \
  refused_before_the_call

check PLG-V16 "a file-parser request names no unenforced field" \
  tests_all_present 'file-parser' 'X-Starport-Unenforced-Provider-Fields' \
    -- internal/protocol/openrouter internal/server

check PLG-V17 "transforms keeps its drop-in behavior and parity stays at 16" \
  transforms_unchanged

check PLG-V18 "the console shows the engine, the page count, the cost, and a cache hit" \
  console_shows_the_cost

# --- Close ---

check PLG-V19 "the operator guide names the plugin, its engines, and the price owner" \
  all_present 'file-parser' 'documents-recognition' \
    -- docs/OPERATOR-GUIDE.md README.md

check PLG-V20 "CI runs this gate and the evidence list names its terminal count" \
  all_present 'verify-document-parser.sh' -- .github/workflows AGENTS.md

printf 'Summary: %d passed, %d failed\n' "$pass" "$fail"
test "$fail" -eq 0
