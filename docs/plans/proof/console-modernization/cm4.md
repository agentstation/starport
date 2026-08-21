# CM4 proof: models catalog page

Date: 2026-08-21. Branch: `codex/cm-4-models` on main @ f934320.

## What landed

- `console/src/routes/models.tsx`: the catalog page. Filter state is
  typed search params (`q`, `provider`, `modality`, `capability`)
  validated by the route, so a filtered view survives reload and
  pastes as a link. Provider options carry model counts
  (`anthropic (14)`). `/` focuses search from anywhere on the page
  (skipped inside inputs/selects/contenteditable). The count reads
  "N of M models". Error, loading, empty-filter (with "Clear
  filters"), and no-key (ConnectCard) states are distinct.
- `console/src/components/models/ModelsTable.tsx`: TanStack Table v9
  (`tableFeatures` + `rowSortingFeature` + `createSortedRowModel`,
  columns through `helper.columns([...])` to keep per-column value
  types) over a `useWindowVirtualizer` list. Dense 40px rows on a
  shared grid template, sticky header, div-based ARIA table
  (`role`, `aria-rowcount`, `aria-sort`). Mono ID chip with
  hover-copy, labeled capability badges (image/audio/file/tools/
  reasoning/structured), context via `formatContext`, prices as
  `$X in · $Y out` mono tabular right-aligned with "—" for unknown
  (never $0). Numeric columns sort with `sortUndefined: "last"` so
  unknown prices and contexts stay at the bottom in both directions.
- `console/src/components/models/FreshnessBar.tsx` +
  `ChangesPanel.tsx` + `ui/SidePanel.tsx`: the catalog freshness
  surface. Generation chip (full ID on hover), relative age,
  completeness/degraded/stale/no-manifest badges, catalog sequence
  and availability revision counters, refresh with inline transient
  notice, and the generation-to-generation diff in a side panel
  (models, offerings grouped by provider, price changes per 1M).
- `console/src/components/shell/Shell.tsx`: the Models nav entry
  flipped to implemented.
- Availability dot deferred: `/api/v1/models` carries no per-model
  availability field; the detail drawer and open-in-chat land with
  CM10. Toast primitive deferred — the freshness bar uses an inline
  notice.

## Evidence

- `pnpm -C console check` → Vite build + `tsc --noEmit` clean.
- `bash scripts/verify-console-modernization.sh` → `Summary: 10
  passed, 11 failed`; CM-V08 newly passes. Fail-before recorded in
  cm0.md.
- Live render (`STARPORT_CONSOLE_NEXT=1`, embedded build, real
  gateway with 422 models):
  - Virtualization active: 36 rendered rows of 422 at rest
    (`aria-rowcount` 422), window updated during real wheel scroll.
  - URL filter persistence: loading
    `/models?provider=anthropic&capability=reasoning&q=opus`
    restored all three controls and showed "5 of 422 models".
  - `/` focused the search input; typing narrowed to 1 row with the
    URL tracking `?q=…`.
  - Sorting: price descending put $510-in whisper first with unknown
    prices last; ascending put "$0 in · $0 out" first; `aria-sort`
    tracked the direction.
  - Freshness bar: generation `catalog-20260819T233…`, "generated 2d
    ago", "no manifest" badge, "catalog sequence 1 · availability
    revision 1". "what changed" opened the side panel with the
    correct empty-history reason ("fewer than two accepted
    generations are recorded; nothing to compare yet"); Escape
    closed it.
- Rendered proof: `cm4-models-dark.jpg`, `cm4-models-light.jpg`.
- Full gate suite on the branch: see the execution log row for CM4.
