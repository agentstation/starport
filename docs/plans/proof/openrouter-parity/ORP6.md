# ORP6 — Console catalog freshness surface

Status: done. PR: [#132](https://github.com/agentstation/starport/pull/132)
(`codex/orp-6-console-freshness` → `codex/orp-5-catalog-freshness`).
Commit `456184a`.

## Fail before

- `TestCatalogFreshnessSurfaceShips` written first and run red:
  `GET /static/js/freshness.js` returned 404 (module absent from the
  embedded assets).
- The conflated display existed at `models.js:111` (as of commit
  `e6c2a56`): `` snapshot ${status.catalog_generation_id} · rev
  ${status.revision ?? "—"} `` — one line mixing the generation ID
  with the availability revision, sourced from the admin-only provider
  status endpoint, with no age, completeness, degradation, or change
  information. The red test also asserts this string's absence.
- The overview "Starmap catalog" card showed only `snapshot` and an
  unnamed `revision`, and rendered only when the admin-scoped provider
  status call succeeded.

## What landed

- Shared module `internal/console/static/js/freshness.js`:
  - `freshnessBadges(metadata)` — completeness badge when not
    `complete` (warn), `degraded` badge with reasons in the tooltip
    (error), `stale` badge past 7 days, `no manifest` badge with the
    endpoint's reason for bootstrap snapshots.
  - `counterText(metadata)` — `catalog sequence N · availability
    revision M`, the two counters named distinctly.
  - `openChangesPanel()` — side panel over
    `GET /api/v1/catalog/changes`: generation pair header, models
    added/removed lists, offerings grouped per provider with
    add/remove counts, per-1M price-change table, the honest
    "semantically equal" message, and the plain reason when no
    history exists yet.
- `models.js`: the snapshot bar became a freshness bar driven by
  `GET /api/v1/catalog` (`models:read`, no longer the admin provider
  status): generation short-ID with the full ID on hover,
  "generated Xm ago", badges, named counters, a "what changed"
  button, and the existing refresh button.
- `overview.js`: the Starmap card loads catalog metadata directly and
  independently of the admin provider-status card, so non-admin keys
  see it; it shows badges, generation, age, both named counters, and
  a what-changed link.
- CSS: badge row, change-list, and change-offering styles.

## Acceptance evidence

1. **Route/regression test**: `TestCatalogFreshnessSurfaceShips` red
   then green — the freshness module ships and reads the changes
   endpoint, `models.js` no longer contains `· rev` and imports the
   shared module, `overview.js` names both counters.
   `go test ./internal/console/ -count=1` ok.
2. **Live walkthrough (rebuilt dev gateway)**: the models bar showed
   `generation 8ea95230-3c7c-4498-b… · generated 19m ago` with live
   `partial` (warn) and `degraded` (error) badges — the current real
   generation is degraded (`source providers observation is
   degraded`) — plus `catalog sequence 1 · availability revision 1`.
   Screenshot `claude-chrome-screenshots-6DpuD6/
   screenshot-1787245387566-4.png`.
3. **Changes panel live**: "what changed" opened the panel showing
   `623ab55f-337e-4539-8… → 8ea95230-3c7c-4498-b… · 19m ago` and "The
   last two generations are semantically equal: no models, offerings,
   or prices changed. Only acquisition metadata differs." Screenshot
   `…-1787245395363-5.jpg`.
4. **Overview card live**: `partial` + `degraded` badges, generation,
   `generated 19m ago`, `catalog sequence 1`, `availability
   revision 1`, what-changed and open-models links. Screenshot
   `…-1787245420984-6.png`.
5. **No new console errors**: the only errors in the tab were stale
   pre-walkthrough entries (the known chat.js TDZ defect deferred to
   ORP12, and extension message-channel noise).
6. **Non-equal diff rendering** (models added/removed, price table):
   implemented and exercised by code review only — the live catalog
   produced no semantic change during the walkthrough window.
   UNVERIFIED live; the underlying diff data is live-proven in ORP5.

## Gates

All on `codex/orp-6-console-freshness` at `456184a`: the seven
`scripts/verify-*.sh` gates exit 0; `go test ./...` exit 0; `go vet`
exit 0; `make lint` exit 0; `make build` exit 0;
`smoke-openrouter-sdks.sh` PASS for Python, TypeScript, and Go.
Autoreview (`--mode branch --base origin/codex/orp-5-catalog-freshness`,
Sol high): clean, 0.99, no findings.

## Deviations and follow-ups

- The stale threshold is 7 days (an embedded bootstrap snapshot can
  predate the install by releases; a daily nag would be noise). The
  plan named no number; revisit if operators want it configurable.
- Per-provider coverage in the changes view is the offerings-grouped-
  by-provider section; per-source acquisition coverage remains on the
  metadata surface (source observations), not duplicated in the panel.
- The refresh button stays visible for non-admin keys; a 403 on click
  reports "Catalog refresh needs an admin-scoped key" (unchanged ORP5
  behavior).
