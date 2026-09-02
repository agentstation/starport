# CPL-E6 proof: usage guardrail fields, export, semantic cache

Branch `codex/cpl-e6`. Base: the CPL-E5 squash `07d5a76`.

## What changed

| Owner | Change |
| --- | --- |
| `internal/usage/model.go` | `Record` gains `cache_similarity` and `cache_semantic`. Records persist as JSON blobs, so the two fields need no migration. |
| `internal/proxy/usage_capture.go` | The chat capture sites read the cache similarity from the response and from the stream, and mark a hit with a similarity above zero as semantic. |
| `internal/usage/repository.go` | `Query` gains `GuardrailVerdict`, and `matches` applies it. |
| `internal/server/controllers/activity.go` | The listing reads a `guardrail` parameter. |
| `internal/server/controllers/activity_export.go` | `AdminExport` serves `GET /api/v1/admin/activity/export` across keys with an optional `key_id`. Both export routes share `exportRecords`. The CSV gains `cache_status`, `cache_semantic`, `cache_similarity`, `guardrail_verdict`, and `guardrail_check`. |
| `internal/server/routes.go` | The admin group registers the export route. |
| `console/src/lib/api.ts` | `ActivityRecord` gains the four fields. `ACTIVITY_RECORD_KEYS` lists every wire key. `ActivityFilters` gains `guardrail`. `activityExportPath` and `exportActivity` fetch the export as a blob under the held credential. `streamChat` accepts extra headers and reads `X-Cache-Similarity`. |
| `console/src/lib/chatStore.ts` | `ChatParams` gains `semanticCache`. `requestHeaders` emits `X-Semantic-Cache: true` when it is on. `ChatStats` gains `cacheSimilarity`. |
| `console/src/components/chat/Composer.tsx` | The parameters popover gains a "Semantic cache" checkbox that names the header it sends. |
| `console/src/routes/chat.tsx` and `Compare.tsx` | Both send paths pass the request headers to `streamChat`. |
| `console/src/components/chat/Messages.tsx` | The cache stat line appends the similarity of a semantic hit. |
| `console/src/routes/usage.tsx` | The status select gains a Guardrail group with "refused" and "redacted". A Guardrail column and a detail row name the verdict and the check. The cache cell reads "semantic" for a semantic hit with the similarity in its tooltip. The Requests card counts semantic hits. Two controls export NDJSON or CSV under the active filters. The column widths fit a 1440px viewport. |
| `console/vitest.config.ts` | The test server may read `internal/usage/model.go`, so one test compares the record keys with the Go JSON tags. |
| `docs/OPERATOR-GUIDE.md` | The export section names the admin route, the `guardrail` filter, and the five new CSV columns. |

## Design notes

The plan names two cache fields: the similarity and a semantic status. The proxy has no semantic status word. Its cache status is `HIT` or `MISS`, and a semantic hit is a `HIT` with a similarity above zero. The record stores that fact as `cache_semantic`, derived at capture, so a reader needs no threshold of its own.

The console session is not a gateway API key. The key-scoped export route reads the records of the caller's key and returns nothing for a session. The console therefore exports through the new admin route, which spans every key and narrows to one with `key_id`. The page fetches the bytes and hands a blob to the browser, because a bearer key never rides a plain link.

The guardrail facet is a backend filter. A client-side filter over the loaded pages would hide the refusals that a later page holds.

## Counts

| Measure | Before | After |
| --- | --- | --- |
| Console tests | 360 | 365 |
| Entry chunk, gzip | 118.67 kB | 118.74 kB |
| Verifier | 36 passed, 12 failed | 39 passed, 9 failed |
| Operator guide lint diagnostics | 48 | 48 |

## Fail-before

At `origin/main` (`07d5a76`) with the E6 test files copied in, V37, V38, and V39 were red and the verifier reported 36 passed, 12 failed. Both tests in `console/src/routes/usage.test.tsx` and the key test in `console/src/lib/activityRecord.test.ts` failed there. The Go tests in `internal/usage`, `internal/proxy`, and `internal/server/controllers` failed to compile there, because `Record` had no `CacheSimilarity` field and the controller had no `AdminExport` method.

## Tests added

| File | Test |
| --- | --- |
| `internal/proxy/usage_capture_test.go` | `TestUsageRecordCarriesSemanticCacheSimilarity` records a semantic hit at 0.93 and an exact hit at zero. `TestStreamingUsageRecordCarriesSemanticCacheSimilarity` records 0.88 through the stream and asserts the wrapper exposes the similarity. |
| `internal/usage/repository_test.go` | `TestListFiltersByGuardrailVerdictAndKeepsCacheFields` filters two refusals and two redactions out of six records and reads the cache fields back. |
| `internal/server/controllers/activity_export_test.go` | `TestAdminExportSpansKeysAndNarrowsToOne` reads three keys as NDJSON and one key as CSV. `TestActivityExportCarriesCacheAndGuardrailColumns` asserts the five new columns and the `guardrail=refuse` filter. |
| `console/src/lib/activityRecord.test.ts` | Every entry of `ACTIVITY_RECORD_KEYS` matches a JSON tag in `internal/usage/model.go`. |
| `console/src/lib/chatStore.test.ts` | The header rides only when the parameter is on. The stats keep the reported similarity. |
| `console/src/routes/usage.test.tsx` | "exports the filtered listing as NDJSON through the admin export route" asserts the fetch URL, the format, the status filter, the absent limit, and the blob handoff. "reads the guardrail facet from the address and names the verdict on the row" asserts the select value, the row text, and the `guardrail=refuse` parameter on the listing. |

## Commands

| Command | Result |
| --- | --- |
| `gofmt -l ./cmd ./internal` | Clean apart from the pre-existing `internal/providers/state/store.go` listing on main. |
| `go vet ./...` | Clean. |
| `go test ./...` | All passed. |
| `pnpm typecheck` | Clean. |
| `pnpm exec vitest run` | 55 files, 365 tests passed. |
| `pnpm build` | Built. Entry chunk 118.74 kB gzip. |
| `bash scripts/verify-console-polish.sh` | 39 passed, 9 failed. V37, V38, and V39 pass. |
| `bash scripts/verify-*.sh` (23 gates, release build gates excluded) | All 23 passed. |
| Technical writing lint on `docs/OPERATOR-GUIDE.md` | 48 diagnostics, the same count as before the edit. |

## Visual check

I rebuilt and relaunched the dev gateway and minted the console session from the launch link. The parameters popover shows the "Semantic cache" checkbox with its header copy. One chat turn through the gateway left an error record, because the dev gateway holds no credential for the selected model. The usage page shows that row with the Guardrail column and the count beside the two export controls. No column clips: the table scroll width equals its client width at 1440px.

A fetch of `/api/v1/admin/activity/export?format=csv` from the page returned status 200, `text/csv`, the 25-column header, and one row. The same route with `guardrail=refuse` returned status 200 and no rows.

UNVERIFIED: a live semantic hit. The dev gateway has no semantic cache layer and no embedding model. The "semantic" cell, the similarity tooltip, and the chat stat line hold unit-test evidence only. UNVERIFIED: a live guardrail verdict, for the same reason. The refused and redacted pills hold unit-test evidence only.
