#!/usr/bin/env bash
# Guard for the asynchronous media jobs campaign (plan prefix AMJ). Each
# condition asserts one structural property of the target design:
#
#   - one job seam holds work that outlives its request, with one transition
#     table and no provider identifier reachable from outside the record,
#   - one provider path submits, polls, and cancels behind a narrow optional
#     interface that a descriptor must satisfy to claim the operation,
#   - four caller routes on both protocol families answer with a Starport job
#     identifier, serve the asset from Starport storage, and expire it,
#   - a job draws its cost once and an account holds a bounded number of them.
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
# state lands, the other four stay behind, and a single-term grep passes.
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

# five_states_one_table holds AMJ-V01. Five constants prove a vocabulary. The
# held fact is a single transition table that every state write passes
# through, walked by a table test over every state pair.
five_states_one_table() {
  all_present JobStateQueued JobStateRunning JobStateCompleted JobStateFailed \
    JobStateCancelled -- internal/jobs || return 1
  tests_all_present CanTransition ErrIllegalTransition -- internal/jobs
}

# video_routes_registered holds AMJ-V09. Each path is registered relative to
# its protocol group, so the absolute path a caller types never appears in the
# source that registers it, and a grep over that source would read a doc
# comment as a route. The held contract is a route test that walks the router
# the server built and names every path a client can reach.
video_routes_registered() {
  local held=internal/server/video_routes_test.go
  [ -f "$held" ] || return 1
  grep -q 'chi.Walk' "$held" || return 1
  all_present '/v1/videos' '/v1/videos/{video_id}' \
    '/v1/videos/{video_id}/content' '/v1/videos/{video_id}/cancel' -- "$held"
}

# console_jobs holds AMJ-V17. A panel that listed jobs and rendered no failure
# reason would satisfy a single-term grep and still leave an operator unable to
# tell a running job from a dead one.
console_jobs() {
  all_present queries.videoJobs submitJob \
    -- console/src/components/jobs/JobsPanel.tsx || return 1
  all_present job-row job-failure job-expired \
    -- console/src/components/jobs/JobsPanel.test.tsx || return 1
  grep_q JobsPanel console/src/routes/jobs.tsx
}

# accounted_once holds AMJ-V14. A usage record written at the terminal state
# is the whole point: a per-poll charge would bill a caller for asking. The
# held fact is a marker on the record that a second accounting pass reads, and
# a test that polls a completed job repeatedly.
accounted_once() {
  local held=internal/jobs/accounting_test.go
  [ -f "$held" ] || return 1
  all_present AccountedAt -- internal/jobs
}

# outstanding_jobs_bounded holds AMJ-V16. The bound belongs to the limits
# vocabulary beside requests, spend, tokens, and stored bytes. Unlike stored
# bytes it is a level a terminal job lowers, so the refusal and the release
# both need a holder.
outstanding_jobs_bounded() {
  grep_q OutstandingJobs internal/limits || return 1
  tests_all_present OutstandingJobs 'http.StatusTooManyRequests' \
    -- internal/server
}

# --- Phase A, the job seam ---

check AMJ-V01 "one transition table names every legal state change" \
  five_states_one_table

check AMJ-V02 "the record exposes no provider job identifier to a caller" \
  in_tests providerJobID internal/jobs

check AMJ-V03 "a job written by one account is unreadable by another" \
  in_tests ErrJobNotFound internal/jobs

check AMJ-V04 "the import graph test names the package and bounds its imports" \
  grep_q 'internal/jobs' internal/architecture

# --- Phase B, the provider path ---

check AMJ-V05 "the catalog projects the video generation operation" \
  all_present 'videos-generations' -- internal/catalog

check AMJ-V06 "the named operation set holds the video operation" \
  all_present OperationVideosGenerations -- internal/routing

check AMJ-V07 "a descriptor claiming the operation with no interface fails activation" \
  in_tests ErrJobsUnsupported internal/providers/connectors

check AMJ-V08 "an unknown provider state word and a spent lifetime both fail loudly" \
  tests_all_present ErrUnknownProviderState ErrJobLifetimeExceeded \
    -- internal/jobs internal/providers/connectors

# --- Phase C, the caller surface ---

check AMJ-V09 "a route test walks the router and names the four video paths" \
  video_routes_registered

check AMJ-V10 "a key holding no videos:write scope cannot submit a job" \
  tests_all_present 'videos:write' -- internal/server

check AMJ-V11 "another account's job answers not found rather than forbidden" \
  tests_all_present ErrJobNotFound 'http.StatusNotFound' -- internal/server

check AMJ-V12 "the content route serves Starport bytes and never a provider URL" \
  tests_all_present AssetKey 'http.StatusFound' -- internal/server

check AMJ-V13 "an expired asset answers 410 and its job keeps an expired marker" \
  tests_all_present AssetExpiredAt 'http.StatusGone' -- internal/jobs internal/server

check AMJ-V14 "a completed job draws exactly one usage record however often a caller polls" \
  accounted_once

check AMJ-V15 "a failed job and a cancelled job draw no cost" \
  tests_all_present JobStateFailed JobStateCancelled \
    -- internal/jobs/accounting_test.go

check AMJ-V16 "an account at its outstanding job limit draws a refusal, and a terminal job frees a slot" \
  outstanding_jobs_bounded

check AMJ-V17 "the console submits a job, names a failure, and marks an expired asset" \
  console_jobs

# --- Close ---

check AMJ-V18 "CI runs this gate and the evidence list names its terminal count" \
  all_present 'verify-async-media-jobs.sh' -- .github/workflows AGENTS.md

printf 'Summary: %d passed, %d failed\n' "$pass" "$fail"
test "$fail" -eq 0
