# CP0 — Baseline

- Date: 2026-08-21
- Baseline pin: `main @ 7e71726` (CM0–CM11 merged; CM12 open on
  `codex/cm-12-compare`, blocked on a provider quota window for its
  proof screenshots).
- Proof root created: `docs/plans/proof/catalog-performance/`.
- Research corpus copied in: 6 documents and 7 review screenshots under
  `research/` (baseline-notes, synthesis, competitor-research,
  starmap-audit, exemplar-hunt, catalog-projection-audit, plus
  review-{overview,providers,models,keys,presets,usage,settings}.jpg).
- Design Review No. 2 artifact:
  <https://claude.ai/code/artifact/c6d042aa-28f0-4c4d-a197-1ba687a02de4>

## Verifier red baseline

`bash scripts/verify-catalog-performance.sh` at `7e71726` (exit 1):

```
FAIL CPV01 authors endpoints are registered on the API router
FAIL CPV02 catalog presentation projection package exists with tests
FAIL CPV03 model projection carries a per-provider offerings table
FAIL CPV04 provider projection populates the description field
FAIL CPV05 gateway serves catalog logos on a dedicated route
FAIL CPV06 console renders identity through an EntityLogo fallback chain
FAIL CPV07 provider detail route exists in the SPA
FAIL CPV08 model detail route exists in the SPA
FAIL CPV09 author list and detail routes exist in the SPA
FAIL CPV10 global command palette component exists
FAIL CPV11 composer no longer embeds the presets popover
FAIL CPV12 composer plus button owns attachments
FAIL CPV13 every proxied response carries the overhead header
FAIL CPV14 usage surfaces the gateway overhead
FAIL CPV15 chat metadata capitalizes TTFT
FAIL CPV16 sidebar wordmark reads STARPORT
FAIL CPV17 navigation labels the keys page API Keys
FAIL CPV18 Starmap pin is v0.7.0 or later
Summary: 0 passed, 18 failed
```

Every condition fails before any campaign work, so each later green is
attributable to its task. The script joins CI at CP18.

## Condition calibration

Each FAIL was calibrated against the current code:

- CPV01: `internal/server/routes.go` has no `/authors` route.
- CPV02–CPV04: `internal/catalog/` has no `view/` package; the proxy
  assembles catalog DTOs inline.
- CPV05: no `/logos/` route in `internal/server/routes.go`.
- CPV06–CPV10: `console/src/` has no `catalog/EntityLogo.tsx`, no
  detail routes, no `authors` routes, no `palette/CommandPalette.tsx`.
- CPV11: `Composer.tsx` calls `listPresets` for its "+" popover.
- CPV12: `Composer.tsx` has no attachment path.
- CPV13: no `x-starport-overhead-ms` anywhere under `internal/`.
- CPV14: no overhead figure in `console/src/routes/usage.tsx`.
- CPV15: `Messages.tsx:242` renders lowercase `ttft`.
- CPV16: `Shell.tsx:138` renders `Starport`, not `STARPORT`.
- CPV17: `Shell.tsx:27` labels the nav item `Keys`.
- CPV18: `go.mod` pins `starmap v0.6.0`.
