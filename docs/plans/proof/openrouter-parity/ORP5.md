# ORP5 — Catalog freshness service and API

Status: done. PR: [#131](https://github.com/agentstation/starport/pull/131)
(`codex/orp-5-catalog-freshness` → `codex/orp-4-usage-page`). Commit
`e6c2a56`.

## Fail before

Captured in `orp5-fail-before.md` (session scratchpad) against the
pre-change tree and dev gateway:

- `GET /api/v1/catalog` → 404.
- `GET /api/v1/catalog/changes` → 404.
- `POST /api/v1/admin/catalog/refresh` → 404.
- `internal/catalog/freshness_test.go` written first: red run recorded
  (compile failures — `FreshnessService`, `SnapshotMetadata`, `Diff`,
  `GenerationStore.History` undefined).
- F6 defect note: the console models-page refresh button called
  `refreshProviders`, which refreshes provider runtime state, not the
  catalog.

## What landed

- `internal/catalog/freshness.go`: `FreshnessService` over a
  `SnapshotSource` and the accepted `GenerationStore`.
  - `Metadata(ctx)`: snapshot scalars always (generation id,
    generated-at, age seconds, catalog sequence, availability revision,
    payload checksum); manifest detail (schema version, payload size,
    completeness, degradation reasons, validation summary, source
    observations, sync run id) from the stored generation record. A
    bootstrap snapshot with no stored record reports
    `manifest_available: false` with an explicit reason — no fabricated
    detail.
  - `Changes(ctx)`: diffs the last two accepted generations — models
    added/removed (definition-ID sets), provider offerings
    added/removed, per-1M price changes over a fixed field order
    (input, output, reasoning, cache_read, cache_write; absent price
    compares as 0). All outputs deterministically sorted. Fewer than
    two stored generations → `available: false` with a reason, HTTP
    200, never an error.
  - Semantic short-circuit: when both index entries carry equal
    semantic checksums, the diff reports `semantically_equal: true`
    without decoding either payload.
- `internal/catalog/generation_index.go` + `generation_store.go`: the
  accepted store appends a capped (32) history index entry
  (generation id, generated-at, payload checksum, semantic checksum
  via `DecodeCatalogPayload` + `CatalogSemanticChecksum`) after the
  pointer swap, on both Commit success paths (clean swap and the
  idempotent already-current path), with 5 CAS retry attempts and
  dedup. The remote store keeps no index (`indexKey == ""`).
- `internal/server/controllers/catalog.go`: `CatalogOperations` port
  and controller. Nil operations → 503 "Catalog operations are not
  configured" (degrade loudly); `context.DeadlineExceeded` → 504;
  `context.Canceled` → 408; `Cache-Control: no-store` on all three
  handlers.
- Routes: `GET /api/v1/catalog` and `GET /api/v1/catalog/changes`
  behind `models:read`; `POST /api/v1/admin/catalog/refresh` in the
  admin group.
- `internal/app/catalog_operations.go`: the app implements the port;
  `RefreshCatalog` snapshots before, runs `refreshRuntime` (real
  acquisition + activation), snapshots after, and reports `changed`
  when the generation id or payload checksum differs, with a
  `catalog refresh completed` info log.
- Console F6 fix: `models.js` refresh now calls the new
  `refreshCatalog()` API and toasts "Catalog updated to generation …"
  or "Catalog is already current".
- Rename: `CatalogDiff` → `catalog.Diff` (revive stutter rule).

## Acceptance evidence

1. **Unit tests green after**: `TestCatalogMetadataExposesManifest`
   (full manifest assertions plus the bootstrap-no-record branch),
   `TestDiffModelsAndPrices` (exact `OfferingChange`/`PriceChange`
   structs; alpha input 1→3, gamma removed, beta added; single-
   generation branch reports `available: false` with reason),
   `TestDiffSkipsProvenanceOnlyChange` (payload checksums proven
   different, diff still `semantically_equal: true` with empty lists),
   `TestCatalogRefreshEndpointActivatesGeneration` and
   `TestCatalogMetadataAndChangesEndpoints` (round-trips, 504 on
   deadline, 503 on nil, no-store header).
2. **Bootstrap honesty (live)**: on the pre-refresh dev gateway,
   `GET /api/v1/catalog` reported the embedded snapshot with
   `manifest_available: false` and reason `no stored generation record
   for "catalog-20260819T233823Z-4b9bfb67cb93"; the snapshot predates
   durable generation storage`.
3. **Real refresh activation (live)**: `POST
   /api/v1/admin/catalog/refresh` ran a real acquisition and activated
   generation `623ab55f-337e-4539-8bb0-f7cf1269aacf`; metadata then
   exposed the full manifest — schema_version 5, payload
   8,333,470 bytes, completeness `partial`, `degraded: true` with
   reason `source providers observation is degraded` (surfaced loudly,
   not hidden), validation passed, 2 source observations, sync_run_id.
4. **Semantic-equal diff (live)**: a second refresh activated
   generation `8ea95230…` with different payload bytes;
   `GET /api/v1/catalog/changes` returned `semantically_equal: true`
   with empty model/offering/price lists — live proof the semantic
   checksum suppresses provenance-only churn.
5. **Refresh log evidence**: two `catalog refresh completed` info
   lines (previous/new generation ids, `changed`) in the serve log.
6. **Scope enforcement (live)**: a `models:read`-only key got 200 on
   `/api/v1/catalog` and `/api/v1/catalog/changes` and 403 on
   `POST /api/v1/admin/catalog/refresh`. Probe keys deleted after.

## Gates

All on `codex/orp-5-catalog-freshness` at `e6c2a56`: the seven
`scripts/verify-*.sh` gates exit 0 (including catalog-driven against
`../starmap`); `go test ./...` exit 0 (37 packages); `go vet` exit 0;
`make lint` exit 0; `make build` exit 0; `smoke-openrouter-sdks.sh`
PASS for Python, TypeScript, and Go. Autoreview (`--mode branch --base
origin/codex/orp-4-usage-page`, Sol high): clean, 0.98, no findings.

## Deviations and follow-ups

- The history index begins at this change; generations accepted before
  it are not back-filled, so the first post-upgrade refresh yields a
  one-entry history and `changes` reports `available: false` until a
  second generation lands. This is the honest behavior, not a defect.
- `Diff` decode work runs on request when semantic checksums differ;
  acceptable at the current catalog size (~8 MB payloads decode in
  well under a second). Revisit with caching only if a console poll
  loop makes this hot.
- The console does not yet render the freshness surface — that is
  ORP6's scope.
- `model_used` double-prefix (`groq/groq/compound-mini`) still open
  (noted since ORP3).
