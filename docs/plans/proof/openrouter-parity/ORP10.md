# ORP10 proof: per-key limits and budgets

- Branch: `codex/orp-10-key-budgets` (stacked on `codex/orp-9-console-routing`)
- Commit: c258499 `feat: add per-key limits and budgets with 402 enforcement`
- PR: https://github.com/agentstation/starport/pull/136 (base `codex/orp-9-console-routing`)

## What shipped

1. Typed `identity.Limits` record (`internal/identity/limits.go`) replaces the
   untyped `rate_limit_config` map on `APIKey`: optional `Requests`
   (`limit` + `window_seconds`) and optional `Spend`/`Tokens` budgets
   (`limit` + `interval` of `day`/`week`/`month`). `Validate`, `IsZero`, and
   `Clone` are nil-safe.
2. Admin key API: create and update accept `allowed_models`, `expires_at`, and
   `limits`; key detail returns current-window consumption per interval plus
   per-budget `used`/`remaining`; key list has real `limit`/`offset`
   pagination with a `has_more` probe (`limit+1` fetch).
3. Per-key request limit overrides the global fixed window in the `rateLimit`
   middleware (`internal/server/rate_limit.go`).
4. Budget enforcement middleware (`internal/server/budget.go`) mounted on both
   `/v1` and `/api/v1` after `rateLimit`: exhausted fixed UTC window returns
   402 with an OpenRouter-shaped "Insufficient quota" body and
   `X-Starport-Budget-{Spend,Tokens}-{Limit,Remaining,Reset}` headers
   (reset = window-end Unix time). Reads `usage.Totals`; newly exported
   `usage.Window` supplies the window end.
5. Dead shapes deleted, not wired: `ratelimit.TokenBucket` and its three error
   sentinels; the config hot-reload subsystem (`internal/config/hot_reload.go`
   + test, app/runtime wiring, factory); inert `RateLimitingConfig` fields;
   `RateLimitsFile` path; the stale `.env.example` section.
6. Key detail exposes consumption via `keyUsage` (windows for day/week/month;
   budgets with `used`/`remaining`); storage read failure per interval maps to
   `{"error": "unavailable"}`.

## Decisions

- **Per-key limit applies when the global default is disabled.** An explicit
  per-key request limit is admin intent, so it enforces even with
  `EnableRateLimiting` off, and beats the global window when both exist.
- **Delete-not-wire (step 5).** Pre-release policy prefers direct breaking
  changes: the unused token bucket and hot-reload subsystem were removed
  instead of being connected.
- **Update-clear semantics.** `"limits": {}` clears all limits;
  `"allowed_models": []` clears the model restriction; absent fields stay
  unchanged; `expires_at` nil = unchanged (no clear path — recorded gap).
- **List pagination scans all identity keys before sorting.**
  `ScanWithPrefix` has no cross-store ordering contract (mock = map order,
  valkey = SCAN order), so stable pagination sorts the full key set and then
  slices. Identity key counts are small.
- **Fail open on budget read errors (D6).** A broken meter must not take the
  gateway down; the failure logs at error level with key, budget, interval.

## Fail-before evidence

With the enforcement body neutered (`next.ServeHTTP(w, r); return` before the
budget checks), both enforcement tests went red:

- `TestSpendBudgetExhaustionReturns402`: expected 402, actual 200.
- `TestTokenBudgetExhaustionReturns402`: expected 402, actual 200.

Restoring the enforcement body made both green.

## Acceptance tests (all green)

- `TestSpendBudgetExhaustionReturns402` — 402, headers, "Insufficient quota"
  + "spend budget exhausted" body.
- `TestTokenBudgetExhaustionReturns402` — 402 at the `>=` boundary.
- `TestBudgetWithinLimitAllowsAndReportsRemaining` — remaining headers
  600000000 spend / 900 tokens.
- `TestBudgetStorageErrorFailsOpen` — Totals error → 200, handler called.
- `TestBudgetMiddlewarePassesKeysWithoutBudgets` — no budgets → pass-through.
- `TestPerKeyRequestLimitOverridesGlobal` — per-key limit 1 beats global 100;
  second request 429; key without override still gets the global window.
- `TestPerKeyRequestLimitAppliesWhenGlobalDisabled` — empty config, per-key
  limit enforces.
- `TestAdminKeySetsLimitsAndExpiry` — create with all three dimensions;
  update replaces limits; `"limits": {}` clears.
- `TestAdminKeyRejectsInvalidLimits` — interval `hour` → 400 naming interval.
- `TestKeyListPagination` — 5 keys paged by 2 with `has_more`; bad limit → 400.

## Verification

- `go test ./...` exit 0; `go vet ./...` exit 0.
- Targeted: `go test ./internal/identity/ ./internal/ratelimit/
  ./internal/server/... ./internal/usage/ -count=1` all ok.
- `make lint` exit 0 (fixed goconst repeats via shared field constants;
  gosec G101 false positive on the Tokens header names suppressed with a
  scoped nolint comment).
- `make build` exit 0.
- All seven `scripts/verify-*.sh` gates exit 0.
- `bash scripts/smoke-openrouter-sdks.sh` PASS for Python, TypeScript, Go.
- Autoreview `--mode branch --base origin/codex/orp-9-console-routing`:
  reviewer codex gpt-5.6-sol thinking=high, clean, no accepted findings,
  overall 0.97.
- Console independence: `internal/console/static/js/api.js` uses only key
  list/create/update/delete endpoints; no `rate_limit_config` references
  remain anywhere.

## Recorded gaps (for later tasks)

- No API path clears `expires_at` once set (nil = unchanged).
- Console budgets UI is ORP11.
