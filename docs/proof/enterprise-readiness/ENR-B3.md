# ENR-B3 proof: usage export

Date: 2026-08-30. Branch: `codex/enr-b3`.

## What shipped

- `internal/usage` owns the `Sink` contract: receive each finalized
  record, batch, flush on a five-second interval, and flush at Close.
- `NewFileSink` appends one NDJSON line per record to a configured path.
  `NewHTTPSink` posts NDJSON batches with three bounded attempts.
- A sink never blocks a request. A full buffer drops the oldest record,
  and a batch that never lands drops whole. Every drop counts on
  `starport_usage_export_dropped_total`. The durable store keeps every
  record either way.
- The sink rides the ENR-B1 observer seam. App composition wraps it as
  a `proxy.UsageObserver`. Every captured record reaches it
  synchronously, before the store write.
- `internal/config` names the target. `STARPORT_TELEMETRY_USAGE_EXPORT`
  takes an http or https URL for the posting sink. Any other value is
  the NDJSON file path.
- `GET /api/v1/activity/export` streams the authenticated key's stored
  records under the `activity:read` scope. It serves NDJSON by default
  and CSV with `format=csv`. It takes the listing's filters.
- `docs/OPERATOR-GUIDE.md` gained a Usage Export section covering both
  paths.

## Acceptance evidence

Named tests, all green:

- `internal/usage`:
  - `TestFileSinkAppendsOneNDJSONLinePerRecord` proves each appended
    line equals the stored record's JSON encoding.
  - `TestHTTPSinkRetriesAFailedPost` proves delivery in exactly three
    attempts: two failures, one success.
  - `TestHTTPSinkCountsDropsWhenTheTargetStaysDown` proves a dead
    target counts every record dropped.
  - `TestSinkDropsOldestWhenTheBufferFills` proves the bound keeps the
    newest records.
- `internal/telemetry`: `TestObserveUsageExportDropsCounts` proves the
  counter adds and the nil surface stays safe.
- `internal/server/controllers`:
  - `TestActivityExportStreamsNDJSONMatchingStoredRecords` proves the
    export scopes to the authenticated key. Each exported line matches
    its stored record.
  - `TestActivityExportServesCSV` proves the header row and the flat
    columns.
  - `TestActivityExportRefusesAnUnknownFormat` and
    `TestActivityExportRequiresAuthentication` hold the refusal paths.

## Commands

- `go test ./...`: pass, no failures.
- `go vet ./...`: clean.
- `make lint`: 0 issues.
- `go build ./...`: clean.
- `bash scripts/benchmark-overhead.sh`: pass.
- `bash scripts/verify-doc-links.sh`: PASS.
- `bash scripts/verify-enterprise-readiness.sh`: `Summary: 6 passed, 27
  failed`. ENR-V01 through ENR-V06 are the green conditions, which
  closes phase B at its exact target.

## Scope notes

- The export endpoint serves the authenticated key's records only. An
  admin-wide export can ride the same handler when a task names it.
- Job accounting records still bypass `UsageCapture`, the ENR-B1 scope
  note. Asynchronous job settlements reach neither the counters nor the
  sink yet.
- No new dependencies.
