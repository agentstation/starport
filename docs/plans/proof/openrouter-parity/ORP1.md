# ORP1 proof: usage record model and repository

- Date: 2026-08-20
- Branch: `codex/orp-1-usage-repo` from `codex/console-revamp@67a800a`.
- Work commit: `eff794f`. PR: #127 (stacked on #125).

## Fail-before evidence

`go test ./internal/usage/` before the change:

```
# ./internal/usage
stat .../internal/usage: directory not found
FAIL ./internal/usage [setup failed]
```

## What shipped

- `internal/usage/model.go`: `Record` with request and key identity,
  timestamp, protocol, operation, models, provider, streaming flag,
  status, `Tokens` (input, output, total, reasoning, cache read, cache
  write), latency, attempts, cache status, and `Cost` in integer
  nano-USD. `Validate` refuses a record that has neither a cost nor a
  `cost_unavailable_reason` (invariant 3: loud degradation).
- `internal/usage/repository.go`: KV repository. Record keys
  `usage:v1:record:<b64 key>:<20-digit unix-nanos>:<b64 request>` give a
  per-key time ordering that survives backend scan-order differences.
  Newest-first `List` with model, provider, status, since, until
  filters and opaque cursor pagination. Atomic aggregate counters
  (requests, tokens, spend) per key and gateway-wide in fixed UTC day,
  week, and month windows, TTL-bounded to window end plus retention.
  Retention default 30 days.
- `internal/architecture/import_graph_test.go`: `internal/usage`
  registered with a storage-only internal import budget.

## Verification

- `go test ./internal/usage/ ./internal/architecture/ -count=1` — ok.
  Contract tests: `TestRepositoryPutAndListByKey`,
  `TestListByKeyOrdersNewestFirstAcrossBackends`,
  `TestListFiltersAndCursorPagination`, `TestAdminListSpansKeys`,
  `TestAggregateCountersAccumulate`,
  `TestCostlessRecordCarriesReasonNotZero`, `TestRetentionTTLApplied`,
  `TestPutRejectsInvalidRecords` — pass on memory and badger; valkey
  subtests skip `UNVERIFIED` (no `TEST_VALKEY_URL`).
- `go test ./...` — 37 packages ok, exit 0. `go vet ./...` — clean.
- Seven repository verify gates — all PASS.
- `make lint` — 0 issues (after promoting counter names to constants).
- `make build` — ok. OpenRouter Python/TypeScript/Go SDK smoke — PASS.
- autoreview, branch mode vs `origin/codex/console-revamp` — clean, no
  accepted findings (`overall: patch is correct (0.99)`).
