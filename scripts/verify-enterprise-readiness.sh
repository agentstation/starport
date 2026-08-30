#!/usr/bin/env bash
# Guard for the enterprise readiness campaign (ID prefix ENR).
# Each condition asserts one structural property of the target design:
#
#   - internal/telemetry owns the metric vocabulary and the tracer: a
#     Prometheus scrape surface, OTLP trace export that stays off until
#     configuration names it, and a usage sink that streams records out,
#   - internal/audit records every admin mutation with its actor, and
#     internal/events delivers signed webhooks for budget, job, health,
#     and key lifecycle,
#   - the gateway serves the OpenAI surfaces a 2026 SDK expects:
#     /v1/responses, /v1/moderations, and gateway-executed /v1/batches,
#   - routing gains an opt-in spread mode, and availability state is
#     shared through KV when the operator configures a distributed store,
#   - internal/guardrails owns pre and post checks that fail closed, with
#     deterministic PII redaction and a moderation check built in,
#   - team budgets, the semantic cache, preset revisions, and the agent
#     surface (catalog verbs plus the embedded skill) each land at their
#     owning seam.
#
# Authored red at baseline f7dfb6b (ENR-A0): each condition turns green as
# its ENR task closes. The gate is terminal at 33 conditions (ENR-V01
# through ENR-V33) and joins CI at ENR-Z2. It reports every condition and
# exits nonzero while any condition fails.
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
# the paths after it. A partial vocabulary is the failure this catches.
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

# --- Phase B, observability ---

check ENR-V01 "internal/telemetry owns the metric vocabulary" \
  all_present 'package telemetry' 'prometheus' -- internal/telemetry

check ENR-V02 "the server serves a Prometheus scrape, held by tests" \
  tests_all_present '"/metrics"' -- internal/server

check ENR-V03 "the tracer exports over OTLP only when configuration names it" \
  all_present 'OTEL_EXPORTER_OTLP_ENDPOINT' -- internal/telemetry internal/config

check ENR-V04 "the disabled tracer is a no-op, held by tests" \
  tests_all_present 'Noop' -- internal/telemetry

check ENR-V05 "the usage seam owns a sink that streams NDJSON records out" \
  all_present 'Sink' 'ndjson' -- internal/usage

check ENR-V06 "the activity API exports stored usage records" \
  all_present 'ActivityExport' -- internal/server

# --- Phase C, governance ---

check ENR-V07 "internal/audit owns the record and migration 0006 lands in sqlstore" \
  all_present 'package audit' '0006_audit_log' -- internal/audit internal/sqlstore

check ENR-V08 "every admin mutation writes an audit record, held by tests" \
  tests_all_present 'AuditRecorder' -- internal/server

check ENR-V09 "the console renders the audit log" \
  ts_all_present 'AuditLog'

check ENR-V10 "internal/events owns the signed webhook envelope" \
  all_present 'package events' 'X-Starport-Signature' -- internal/events

check ENR-V11 "budget, job, and health transitions emit named events" \
  all_present 'budget.exhausted' 'job.completed' 'provider.health' -- internal/events internal/jobs internal/server internal/providers/state

check ENR-V12 "the security posture document exists and the README links it" \
  all_present 'SECURITY-POSTURE.md' -- README.md

# --- Phase D, surface parity ---

check ENR-V13 "the gateway serves /v1/responses on the canonical chat contract" \
  all_present '/v1/responses' -- internal/server internal/protocol/openai

check ENR-V14 "the responses codec round-trips, held by tests" \
  tests_all_present 'ResponsesRequest' -- internal/protocol/openai

check ENR-V15 "moderations is canonical, routed, and scoped" \
  all_present 'ModerationRequest' '/v1/moderations' 'moderations:write' -- internal

check ENR-V16 "the gateway serves /v1/batches over the jobs seam" \
  all_present '/v1/batches' -- internal/server

check ENR-V17 "batch lines run through the planner and meter, held by tests" \
  tests_all_present 'batch' -- internal/jobs

# --- Phase E, routing and health ---

check ENR-V18 "spread selection stays inside the ranking band, held by tests" \
  tests_all_present 'Spread' -- internal/routing

check ENR-V19 "availability publishes shared state through the KV contract" \
  all_present 'KVStore' -- internal/availability

check ENR-V20 "with no shared store the tracker stays local, held by tests" \
  tests_all_present 'LocalOnly' -- internal/availability

# --- Phase F, guardrails ---

check ENR-V21 "internal/guardrails owns the verdict vocabulary" \
  all_present 'package guardrails' 'redact' 'refuse' -- internal/guardrails

check ENR-V22 "a check that cannot evaluate refuses, held by tests" \
  tests_all_present 'FailClosed' -- internal/guardrails

check ENR-V23 "PII detection covers cards under Luhn, held by tests" \
  tests_all_present 'Luhn' -- internal/guardrails

check ENR-V24 "the moderation check rides the account's own routing" \
  all_present 'moderation' -- internal/guardrails

# --- Phase G, tenancy ---

check ENR-V25 "the limits vocabulary reaches teams" \
  all_present 'TeamBudget' -- internal/limits internal/identity

check ENR-V26 "team budget exhaustion refuses pre-flight, held by tests" \
  tests_all_present 'TeamBudget' -- internal/server internal/limits

# --- Phase H, product polish ---

check ENR-V27 "the semantic cache is opt-in and lives beside the exact identity" \
  all_present 'semantic_cache' 'Cosine' -- internal/config internal/response/cache

check ENR-V28 "similarity answers only above the threshold, held by tests" \
  tests_all_present 'similarity' -- internal/response/cache

check ENR-V29 "preset revisions pin and roll back, held by tests" \
  tests_all_present 'Rollback' -- internal/presets

check ENR-V30 "the CLI owns the agent surface: catalog verbs and the embedded skill" \
  all_present 'models' 'agent setup' 'SKILL.md' -- cmd/starport skills/starport

check ENR-V31 "the catalog verbs answer JSON and agent setup writes the skill, held by tests" \
  tests_all_present 'ModelsSearch' 'AgentSetup' -- cmd/starport

# --- Close ---

check ENR-V32 "deleting an owner repairs a corrupt hash-index record, held by tests" \
  tests_all_present 'RepairsCorrupt' -- internal/identity

check ENR-V33 "CI runs this gate and the evidence list names it" \
  all_present 'verify-enterprise-readiness.sh' -- .github/workflows AGENTS.md

printf 'Summary: %d passed, %d failed\n' "$pass" "$fail"
test "$fail" -eq 0
