# ENR-H2 proof: preset revisions

Status date: 2026-09-01.

## What shipped

A preset save destroyed its history. An edit under a live client changed
behavior with no way back. This task makes every save an immutable
revision with an incrementing number, adds pinned resolution, and adds
rollback.

The existing optimistic-concurrency counter and the history number
reconcile into one value. Every save is a new revision, so the number a
caller must name on an update is also the head's place in the history.
The two meanings never diverge, so the task renamed nothing.

## The pieces

- `internal/presets/repository.go`: the `Repository` contract gains
  `History`, `GetRevision`, and `Rollback`. `Record` documents the
  reconciled revision meaning. Create, Update, and Delete keep their
  head semantics.
- `internal/presets/revisions.go`: revision snapshots live under
  `presets:v1:revision:`, keyed by name and a zero-padded number, so a
  prefix scan lists in order. The head write stays authoritative. A
  snapshot write follows it and fails open with a loud log. Delete
  drops the history best-effort after the head leaves.
- `internal/proxy/presets.go`: `@preset/name` resolves the latest
  revision, and `@preset/name@N` pins one. Both the model-field
  reference and the OpenRouter `preset` body field accept the pin.
  Preset names never contain `@`, so the split is unambiguous. A bad
  pin fails like an unknown preset, before any routing happens.
- `internal/server/controllers/presets.go` and
  `internal/server/routes.go`: two routes join the preset block. Any
  authenticated key reads `GET /api/v1/presets/{name}/history`, newest
  first. `POST /api/v1/presets/{name}/rollback` needs `presets:write`.
  It names the head it read and lands in the audit log as
  `preset.rollback`.
- `console/src/routes/presets.tsx` and `console/src/lib/api.ts`: each
  preset row opens a history view with the pin syntax and a restore
  action per old revision.
- `docs/OPERATOR-GUIDE.md` and `docs/ARCHITECTURE.md`: a Preset
  Revisions section documents the pin, the routes, and the conflict
  rule. The namespace list names the new revision prefix.

## Acceptance evidence

- Three saves yield revisions 1 through 3, and the history answers
  newest-first: `TestPresetRevisionHistoryAndRollback`.
- A pinned request uses revision 2 verbatim:
  `TestPresetRevisionHistoryAndRollback`,
  `TestPinnedPresetReferenceUsesTheRevision`, and the end-to-end pin in
  `TestChatResolvesPresetReference`.
- Rollback to revision 1 creates revision 4 equal to it, and a stale
  expected revision conflicts: `TestPresetRevisionHistoryAndRollback`
  and `TestPresetHistoryAndRollbackEndpoints`.
- A rollback without a target refuses: `TestPresetRollbackNeedsATarget`.
- A bad pin never reaches routing:
  `TestPinnedPresetReferenceRejectsBadPins`.
- Delete drops the history with the head:
  `TestPresetRevisionHistoryAndRollback`.
- The scope split holds: a reader key reads history and cannot roll
  back: `TestPresetHistoryAndRollbackEndpoints`.

## Checks

- `go test ./internal/presets/ ./internal/proxy/ ./internal/server/...
  ./internal/app/`: pass.
- `go test -race ./internal/presets/ ./internal/proxy/`: pass.
- Console: `pnpm check` passes with 33 test files and 213 tests.
- `bash scripts/verify-enterprise-readiness.sh`: 29 passed, 4 failed.
  ENR-V29 is green. The four failures are the tasks that remain:
  ENR-V30 through ENR-V33.
- The full pre-PR battery from the repository evidence list: pass. Each
  optional SDK smoke check reports its own skip status in CI.
- `technical-writing lint docs/OPERATOR-GUIDE.md`: the new section is
  clean, and the file keeps its 48 baseline diagnostics.
