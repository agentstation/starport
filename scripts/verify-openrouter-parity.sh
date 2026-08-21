#!/usr/bin/env bash
# Verifier for docs/plans/openrouter-parity-plan.html.
# Each condition asserts one shipped capability of the OpenRouter parity
# campaign. The script stays red until the campaign closes (ORP13). It
# reports every condition and exits nonzero while any condition fails.
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

check ORP-V01 "internal/usage owns the request record repository" \
  test -f internal/usage/repository.go
check ORP-V02 "usage storage namespace is versioned usage:v1:" \
  grep_q 'usage:v1:' internal/usage
check ORP-V03 "proxy completion path writes usage records" \
  grep_q 'usage.Record' internal/proxy
check ORP-V04 "GET /api/v1/activity route is registered" \
  grep_q '/activity' internal/server/routes.go
check ORP-V05 "admin activity route is registered" \
  grep_q 'admin.*activity\|activity.*requireAdmin' internal/server/routes.go
check ORP-V06 "console serves a /usage page" \
  grep_q '"/usage"' internal/console/handler.go
check ORP-V07 "catalog snapshot metadata route is registered" \
  grep_q '/catalog' internal/server/routes.go
check ORP-V08 "catalog refresh endpoint exists" \
  grep_q 'catalog/refresh' internal/server/routes.go
check ORP-V09 "console catalog button calls the catalog refresh endpoint" \
  grep_q 'catalog/refresh' internal/console/static/js
check ORP-V10 "preset routes are registered" \
  grep_q 'presets' internal/server/routes.go
check ORP-V11 "@preset/ request references resolve" \
  grep_q '@preset/' internal/proxy internal/protocol
check ORP-V12 "provider.sort reaches the routing policy" \
  grep_q 'Sort' internal/server/controllers/chat.go
check ORP-V13 "max_price rejection code exists in routing" \
  grep_q 'price_exceeded' internal/routing
check ORP-V14 "admin key API accepts allowed_models" \
  grep_q 'allowed_models' internal/server/controllers/admin.go
check ORP-V15 "budget exhaustion has a 402 regression test" \
  grep_q 'TestSpendBudgetExhaustionReturns402' internal/server
check ORP-V16 "chat page has a comparison mode" \
  grep_q 'compareMode' internal/console/static/js

printf 'Summary: %d passed, %d failed\n' "$pass" "$fail"
test "$fail" -eq 0
