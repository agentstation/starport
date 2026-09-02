# CPL-E5 proof: audit log as an investigation tool

Branch `codex/cpl-e5`. Base: the CPL-E4 squash `9e5e7b7`.

## What changed

| Owner | Change |
| --- | --- |
| `internal/audit/audit.go` | `Record` gains `RequestID`, the gateway request that carried the mutation. It stays empty for a write without a request context. |
| `internal/audit/repository.go` | The insert, the select, and the scan carry `request_id`. |
| `internal/sqlstore/migrations/*/0007_audit_request_id.sql` | Each dialect adds the `request_id` column with an empty default. The MySQL file states how to recover from a partial run, because MySQL has no `IF NOT EXISTS` on a column. |
| `internal/server/controllers/audit.go` | `writeAudit` reads the chi request ID from the context, so every one of its 28 callers fills the field. |
| `internal/usage/repository.go` and `controllers/activity.go` | `usage.Query` gains `RequestID`, and the activity listing reads `request_id`, so a request ID selects one usage row. |
| `console/src/lib/timeRange.ts` | The range vocabulary left `routes/usage.tsx` so the audit page and the usage page read one set of labels and windows. |
| `console/src/lib/api.ts` and `lib/queries.ts` | `AuditFilters` gains `until`, `ActivityFilters` gains `request_id`, and `queries.audit` keys on the whole filter object. |
| `console/src/routes/audit.tsx` | Filters live in search params: `actor`, `action`, `range`, and `until`. The default range is the last 30 days. The Request column links to the usage page filtered by the request ID. The outcome renders as a chip. The actor renders as the name after its kind prefix, with the raw actor in a tooltip. An empty window offers "Show all time" and "Clear filters". |
| `console/src/routes/usage.tsx` | A request ID filter joins the model, provider, and key filters, so the audit link lands on one row. |
| `docs/OPERATOR-GUIDE.md` | The audit section names `request_id` and the activity filter list gains it. |

## Design notes

The usage page records inference requests. An admin mutation leaves an audit record and no usage row. Its request link opens an empty usage listing with the request ID in the filter. The link still gives a reader the ID to search in the request log. A mutation that a request also charged, such as a moderation call, lands on its usage row.

The actor cell shows `ticket` for a console session minted from a launch ticket and `ci-deployer` for a key with that name. The tooltip shows `console:ticket` and `key:ci-deployer`. An `anonymous` actor has no prefix and shows as it is.

## Counts

| Measure | Before | After |
| --- | --- | --- |
| Console tests | 356 | 360 |
| Entry chunk, gzip | 118.69 kB | 118.67 kB |
| Verifier | 35 passed, 13 failed | 36 passed, 12 failed |
| Operator guide lint diagnostics | 48 | 48 |

## Fail-before

At `origin/main` (`93dc1bc`) with the E5 test files copied in, V36 was red and the verifier reported 34 passed, 14 failed. The four tests in `console/src/routes/audit.test.tsx` failed there. `TestAuditRecordCarriesRequestID` and the request assertion in `TestKeyLifecycleRecordsTheConsoleActor` failed to compile there, because `Record` had no `RequestID` field. The E4 change touches no audit file, so the same base holds after the E4 merge.

## Tests added

| File | Test |
| --- | --- |
| `internal/audit/repository_test.go` | `TestAuditRecordCarriesRequestID` records one entry with `req-42` and one without, and reads both back in order. |
| `internal/server/controllers/audit_test.go` | `TestKeyLifecycleRecordsTheConsoleActor` now asserts the create record carries the request ID from the context and the delete record carries none. |
| `internal/server/controllers/activity_test.go` | `TestActivityFiltersAndPagination` now asserts `?request_id=req-2` returns the one record. |
| `internal/sqlstore/sqlstore_contract_test.go` | `TestAuditLogMigrationShips` now requires `0007_audit_request_id.sql` in every dialect. |
| `console/src/routes/audit.test.tsx` | "carries the action filter into the query key and the request" asserts the fetch URL carries `action=key.create` and the query key is `["audit", {action, limit}]`. "links each record to its request on the usage page" asserts the link href starts with `/usage?` and carries `request=req-42`. "names the actor and states the outcome" asserts `ci-deployer` shows and `key:ci-deployer` does not. "offers a wider range when the window is empty" asserts the 30 day message, the "Show all time" button, and a `since` parameter on the request. |

## Commands

| Command | Result |
| --- | --- |
| `gofmt -l`, `go vet` on the four touched packages | Clean. The pre-existing `internal/providers/state/store.go` listing is unchanged from main. |
| `go test ./internal/audit/... ./internal/server/... ./internal/usage/... ./internal/sqlstore/...` | All passed. |
| `pnpm typecheck` | Clean. |
| `pnpm exec vitest run` | 52 files, 360 tests passed. |
| `pnpm build` | Built. Entry chunk 118.67 kB gzip. |
| `bash scripts/verify-console-polish.sh` | 36 passed, 12 failed. V36 passes. |
| `bash scripts/verify-*.sh` (23 gates, release build gates excluded) | All 23 passed. |
| Technical writing lint on `docs/OPERATOR-GUIDE.md` | 48 diagnostics, the same count as before the edit. |

## Visual check

The check ran on the vite dev server at `127.0.0.1:5174` against a rebuilt dev gateway on port 8080 that carried the Go changes.

| Step | Result |
| --- | --- |
| Filter bar | Actor and action inputs, the range select at "last 30 days", and the "until" instant input render in one row. |
| Records | A throwaway key `audit-check` left a `key.create` and a `key.delete` record. Each row showed the actor `ticket`, the action, the key ID, the request ID as a link, and a green `ok` chip. The footer read "2 records loaded". |
| Actor tooltip | Hovering `ticket` showed `console:ticket`. |
| Request link | The link opened the usage page with the request ID in its filter and "all time" as the range. The listing was empty, as the design note states. |
| Empty window | `/audit?action=account.delete&range=1h` showed "No records in the last hour match these filters." with "Show all time" and "Clear filters". |

The throwaway key left the dev gateway after the check.
