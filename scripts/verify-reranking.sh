#!/usr/bin/env bash
# Guard for the reranking campaign (plan prefix RNK). Each condition asserts
# one structural property of the target design:
#
#   - Starmap owns the rerank operation, the offerings, the search unit price,
#     and the document limit, and Starport reads all four from one snapshot,
#   - one canonical rerank shape carries no wire word, and a result names a
#     document by its index rather than by a copy of its text,
#   - two codecs own every wire name: the Cohere-shaped one at /v1/rerank and
#     the OpenRouter-shaped one at /api/v1/rerank,
#   - a rerank request reaches only an offering that serves the operation, and
#     the refusal names the operation rather than a provider error,
#   - a rerank turn counts search units, draws a price, and obeys the same
#     spend bound every other operation obeys.
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

# all_present reports that every term before the -- appears somewhere under
# the paths after it. A partial vocabulary is the failure this catches: the
# route lands, the scope stays behind, and a single-term grep passes.
all_present() {
  local terms=() paths=()
  local seen=0 arg
  for arg in "$@"; do
    if [ "$arg" = "--" ]; then seen=1; continue; fi
    if [ "$seen" -eq 0 ]; then terms+=("$arg"); else paths+=("$arg"); fi
  done
  if [ "$seen" -eq 0 ] || [ "${#paths[@]}" -eq 0 ]; then return 1; fi
  local term
  for term in "${terms[@]}"; do
    grep -Rq -- "$term" "${paths[@]}" || return 1
  done
  return 0
}

# tests_all_present is all_present restricted to Go test files. A symbol that
# only the source names is a vocabulary entry, not a held contract.
tests_all_present() {
  local terms=() paths=()
  local seen=0 arg
  for arg in "$@"; do
    if [ "$arg" = "--" ]; then seen=1; continue; fi
    if [ "$seen" -eq 0 ]; then terms+=("$arg"); else paths+=("$arg"); fi
  done
  if [ "$seen" -eq 0 ] || [ "${#paths[@]}" -eq 0 ]; then return 1; fi
  local term
  for term in "${terms[@]}"; do
    grep -Rq --include='*_test.go' -- "$term" "${paths[@]}" || return 1
  done
  return 0
}

# ts_all_present is all_present restricted to the console sources.
ts_all_present() {
  local term
  for term in "$@"; do
    grep -Rq --include='*.ts' --include='*.tsx' -- "$term" console/src || return 1
  done
  return 0
}

# no_local_operation_table refuses a rerank operation constant outside the one
# seam that names operations at all. Invariant R1: internal/routing restates
# Starmap's operation names to stay free of the catalog import, and a contract
# test in internal/providers/connectors holds the two lists equal. A third
# spelling in the catalog projection or in the canonical types is an unheld
# source that drifts from both.
no_local_operation_table() {
  ! grep -Rq --include='*.go' -e 'Operation[A-Za-z]* *= *"rerank"' \
    internal/catalog internal/inference
}

# rerank_operation_from_starmap is RNK-V01 in full: Starport names the Starmap
# operation, and it declares no competing constant of its own.
rerank_operation_from_starmap() {
  all_present 'ProviderOperationRerank' -- internal && no_local_operation_table
}

# --- Phase A, the catalog fact ---

check RNK-V01 "Starport reads the rerank operation from Starmap and declares none of its own" \
  rerank_operation_from_starmap

check RNK-V02 "a model that serves rerank alone stays out of the chat model list" \
  tests_all_present 'ProviderOperationRerank' -- internal/catalog

check RNK-V03 "the shipped catalog holds a rerank offering that carries a search unit price" \
  tests_all_present 'SearchUnit' 'ProviderOperationRerank' -- internal/catalog

check RNK-V04 "every rerank offering states the document count it accepts" \
  all_present 'MaxDocuments' -- internal/catalog internal/inference

# --- Phase B, the request path ---

check RNK-V05 "the canonical rerank shape names a document by index and carries no wire word" \
  tests_all_present 'RerankResult' 'Index' -- internal/inference

check RNK-V06 "the transport registry accepts a rerank descriptor and still refuses an unknown operation" \
  tests_all_present 'ProviderOperationRerank' -- internal/providers/connectors

# RNK-V07 reads the connectors package rather than internal/failure. Decision
# RNK-D13 records why: internal/failure owns the failure vocabulary and imports
# no provider code, so the package that normalizes a provider rejection is the
# package that holds the contract.
check RNK-V07 "a connector without the operation refuses, and a provider error normalizes" \
  tests_all_present 'Rerank' 'NormalizeFailure' -- internal/providers/connectors

check RNK-V08 "the /v1 codec round-trips the published rerank request and response" \
  tests_all_present 'relevance_score' 'top_n' -- internal/protocol/openai

check RNK-V09 "an echoed document comes from the request, and an out-of-range score fails decoding" \
  tests_all_present 'return_documents' -- internal/protocol/openai

check RNK-V10 "POST /v1/rerank is registered behind the rerank:write scope" \
  all_present '"/rerank"' 'rerank:write' -- internal/server/routes.go

check RNK-V11 "the anonymous identity carries rerank:write when an operator disables authentication" \
  all_present 'rerank:write' -- internal/apikey/anonymous.go

check RNK-V12 "an uncatalogued rerank model is refused before any provider call" \
  tests_all_present 'rerank' -- internal/server

# RNK-V21 and RNK-V22 belong to phase B. RNK0 added them after reading the
# live OpenRouter schema, which publishes a rerank route the plan had recorded
# as absent. The numbering keeps the earlier conditions stable.
check RNK-V21 "POST /api/v1/rerank mirrors the published OpenRouter rerank schema" \
  tests_all_present 'search_units' -- internal/protocol/openrouter

check RNK-V22 "the parity gate counts the rerank route and states its terminal count" \
  all_present 'rerank' -- scripts/verify-openrouter-parity.sh

# --- Phase C, policy and money ---

check RNK-V13 "the planner refuses a model that serves no rerank offering" \
  tests_all_present 'ProviderOperationRerank' -- internal/routing

check RNK-V14 "the refusal names the model and the operation rather than a provider error" \
  all_present 'ErrOperationUnsupported' -- internal/routing

# Decision RNK-D19 moved the three seams below. The record field belongs to
# internal/usage, but the meter that fills it and the bound that reads its
# price both live where the request is, so a test in internal/usage would hold
# an arithmetic no request runs through.
check RNK-V15 "a rerank turn records a search unit count and a nonzero cost" \
  tests_all_present 'SearchUnits' 'record.Cost' -- internal/proxy

check RNK-V16 "an account at its spend bound is refused before the provider call" \
  tests_all_present 'LowestSearchUnitPrice' 'ErrSpendLimitExceeded' -- internal/proxy

check RNK-V17 "a turn on an unpriced rerank offering fails rather than reporting zero" \
  tests_all_present 'ErrRerankUnpriced' 'RouteExclusionOperationUnpriced' 'CostReasonRerankUnpriced' \
  -- internal/catalog internal/proxy

check RNK-V18 "the console names the operation, the search unit price, and the document limit" \
  ts_all_present 'rerank' 'search_unit'

# --- Close ---

check RNK-V19 "the operator guide and the README name the route, the scope, and the price owner" \
  all_present '/v1/rerank' 'rerank:write' -- docs/OPERATOR-GUIDE.md README.md

check RNK-V20 "CI runs this gate and the evidence list names its terminal count" \
  all_present 'verify-reranking.sh' -- .github/workflows AGENTS.md

printf 'Summary: %d passed, %d failed\n' "$pass" "$fail"
test "$fail" -eq 0
