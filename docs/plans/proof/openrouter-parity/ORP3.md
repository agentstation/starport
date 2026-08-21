# ORP3 proof — activity API, real metrics, provider usage

- Branch: `codex/orp-3-activity` (from `codex/orp-2-usage-capture`).
- PR: #129 (base `codex/orp-2-usage-capture`).
- Commit: `feat: serve request activity, real metrics, and provider usage`
  (aa4dd9e).

## Fail-before evidence

Acceptance tests were written first. Before the implementation landed:

```
$ curl -s -w '\nstatus:%{http_code}\n' -H "Authorization: Bearer $KEY" \
    http://localhost:8080/api/v1/activity
{"error":{"code":404,"message":"The requested endpoint does not exist",...}}
status:404

$ go test ./internal/server/... -run 'Activity|Metrics|ProviderKeyUsage' -count=1
activity_test.go: undefined: NewActivityController (3 call sites)
activity_test.go: too many arguments in call to NewAdminController
activity_test.go: too many arguments in call to NewProviderKeysController
FAIL github.com/agentstation/starport/internal/server/controllers [build failed]
```

## What landed

- `internal/server/controllers/activity.go`: `ActivityController`.
  `GET /api/v1/activity` (scope `activity:read`) forces the query to the
  authenticated key from `requestctx`; `GET /api/v1/admin/activity`
  (admin) accepts an optional `key_id` filter. Filters: `model`,
  `provider`, `status`, `since`/`until` (RFC3339, parse error → 400),
  `limit` (positive integer → 400 otherwise), `cursor`. Responses use a
  new shared `dto.WriteList` envelope `{"data": [...], "next_cursor"}`.
  `usage.ErrInvalidQuery` → 400. Nil repository → 503 "Usage accounting
  is not configured" (loud degradation, matching D-series decisions).
- `internal/server/controllers/admin.go`: `Metrics` now reads real data.
  A 24-hour sample (`usage.MaxListLimit`) drives `requests`
  {total, success, errors, rate_1min}, latency p50/p95/p99
  (nearest-rank), `tokens.total`, `spend` {nano_usd, currency,
  requests_without_cost}, and per-provider request/error counts (empty
  provider → `unrouted`). Exact day/week/month counters from
  `Repository.Totals` ride along as `windows`; a `sample` block reports
  {records, window, truncated} so the sample basis is honest.
- `internal/server/controllers/provider_keys.go`: `GetUsage` replaces
  its 501 with per-provider aggregates over recorded usage (30-day
  window, bounded cursor walk of 30 × 1000 records, `truncated` flag on
  overflow): requests, errors, all six token sums, `spend_nano_usd`,
  `requests_without_cost`. `GetUsageComparison` retires with `410 Gone`
  pointing to `/api/v1/activity` and `/usage/provider-keys`. No 501
  remains under `/usage/`.
- Wiring: `server.Dependencies.Usage` (documented nil-degrades-to-503,
  deliberately not a required dependency), `Controllers.Activity`,
  routes + route documentation, `internal/app` passes `b.usageRecords`.
- Console client: `listActivity` / `listAdminActivity` helpers in
  `internal/console/static/js/api.js` (consumed by ORP4).

## Acceptance evidence (fail-after)

```
$ go test ./internal/server/... -run 'Activity|Metrics|ProviderKeyUsage' -count=1 -v
--- PASS: TestActivityListsOwnKeyOnly        (own-key forcing + 401 unauthenticated)
--- PASS: TestActivityFiltersAndPagination   (model/status/provider filters,
                                              limit=1 cursor walk newest-first,
                                              bad since → 400, bad limit → 400)
--- PASS: TestAdminActivityFiltersByKey      (all keys, then ?key_id=key-b)
--- PASS: TestAdminMetricsReflectRecordedUsage (3 records: total 3, success 2,
                                              errors 1, p50 300, p95/p99 500,
                                              tokens 450, spend 2000000 USD,
                                              providers openai{2,1} groq{1,0})
--- PASS: TestProviderKeyUsageAggregates     (priced+unpriced records, foreign
                                              key excluded, comparison → 410)
ok      github.com/agentstation/starport/internal/server/controllers
```

## Live transcript (dev gateway, 2026-08-20)

Run against the working-tree binary over the `starport init` badger
store, with real provider credentials from the local environment.

- `POST /api/v1/chat/completions` model `groq/compound-mini` → 200,
  provider groq, usage 443 prompt / 34 completion / 477 total. **This
  settles the ORP2 live check recorded UNVERIFIED in ORP2.md**: one chat
  request produced exactly one listed record.
- `GET /api/v1/activity?limit=2` → 200 list envelope. Record carries
  request_id, key_id, protocol `openrouter`, operation `chat`,
  model_requested `groq/compound-mini`, provider `groq`, status `ok`,
  tokens {443, 34, 477}, latency_ms 788, routing_ms 705, attempts 1,
  cache_status MISS, and `cost_unavailable_reason: "no_pricing"` —
  Starmap has no compound-mini price, so per D2 the cost is absent with
  a reason, never zero.
- `GET /api/v1/admin/activity?key_id=...&limit=1&status=ok` → 200, key
  filter honored, cursor present.
- `GET /api/v1/admin/metrics` → real numbers: requests {total 10,
  success 2, errors 8}, latency p50 60 / p95 1626 / p99 1626,
  tokens.total 1030, providers {groq {2, 0}, unrouted {8, 8}}, windows
  from exact counters, sample {records 10, window 24h, truncated false}.
  (The 8 errors are the debug attempts from bring-up — honest data.)
- `GET /api/v1/keys/{key_id}/usage/provider-keys` → 200 aggregates:
  groq {requests 2, tokens 1030, requests_without_cost 2}, unrouted
  {requests 8, errors 8}, 30-day window in RFC3339.
- `GET /api/v1/keys/{key_id}/usage/comparison` → 410 with the
  replacement pointer.

## Required gates

- Seven `scripts/verify-*.sh` gates: all exit 0.
- `go test ./...`: green (37 packages ok).
- `go vet ./...`, `make lint` (0 issues), `make build`: green.
- `scripts/smoke-openrouter-sdks.sh`: PASS Python, PASS TypeScript,
  PASS Go.
- Autoreview: Sol (gpt-5.6-sol, thinking=high), branch mode against
  `origin/codex/orp-2-usage-capture`, TruffleHog clean, verdict "patch
  is correct (0.96)", no findings.

## Deviations and open items

- The task sketch said "per-credential usage" for
  `/usage/provider-keys`; records carry the provider that served each
  request, not which credential authenticated it, so the grouping is
  per provider. Per-credential attribution would need a record-schema
  change — out of ORP3 scope.
- No default-scope constant list exists in the codebase to add
  `activity:read` to; setup-minted keys carry the `*` wildcard, which
  passes `requireAnyScope`. Recorded here so ORP10 (key limits) can
  revisit scope defaults deliberately.
- The two legacy 501-placeholder tests were rewritten to the new
  contract (503 without a repository; 410 for comparison). That is the
  task's contract change, not test weakening.
- Live observation for follow-up: `model_used` records as
  `groq/groq/compound-mini` (provider-prefixed provider_model_id from
  response evidence) — normalization candidate.
- Live observation: `openai/gpt-oss-20b` routes to a credential-less
  provider → 401. Routing preference work is ORP8.
