# CPL-D2 Shared data table and five conversions

Branch `codex/cpl-d2`. Base `a7f904b` (main after CPL-D1).

## What changed

One table component now owns sorting, sticky headers, column resizing, row
selection, virtualization, and the keyboard path. Five consumers render
through it. The table names each owner and its change.

| Owner | Change |
| --- | --- |
| `components/ui/DataTable.tsx` | New. Exports `DataTable`, `DataTableFooter`, `dataColumns`, `dataTableFeatures`, `selectionColumn`, and `VIRTUAL_THRESHOLD`. Column definitions carry `cell` renderers, and every cell renders through `flexRender`. |
| Header | A `columnheader` carries `aria-sort` only when the column sorts. The sort control is a `button` with the arrow on hover, and a `separator` handle resizes the column. The header row sticks to the top and follows the body's horizontal scroll. |
| Body | The body scrolls horizontally in its own container. Above 100 rows a window virtualizer positions the rows. A layout effect and a resize observer on `document.body` keep the scroll margin current. |
| Rows | Each row carries `aria-rowindex` from 2, and the table carries `aria-rowcount` with the header row. An activatable row takes `tabIndex`, Enter, Space, and an inset accent focus stripe. A click on an inner link, button, or input never activates the row. |
| `TableMeta` and `ColumnMeta` | Module augmentation adds `align`, `flex`, and `className` to a column, and a per-page `meta` slot for callbacks. |
| `ModelsTable.tsx` | Columns own their cells. The page passes `onRowActivate` for the detail route and keeps the name cell as a `Link`. Default widths now sum to 1,100 px so the table fits a 1,440 px viewport beside the sidebar. The price cell no longer wraps. |
| `routes/audit.tsx` | Module-level columns and a footer with the loaded count, the request bound, and a load-older control on the cursor. |
| `routes/keys.tsx` | Module-level columns with callbacks through `meta`. Default widths sum to 1,120 px. |
| `routes/presets.tsx` | Same pattern. Default widths sum to 1,040 px. |
| `ProviderDetail.tsx` | The offerings table sorts and carries Circuit and Routing facet filters with counts and a visible-of-total count. |
| `DocumentsPanel.tsx`, `FilesPanel.tsx` | The same footer states the loaded count and the request bound. `FILE_PAGE_LIMIT` and `DOCUMENT_ACTIVITY_LIMIT` are now exported constants. |

The plan step names `scope="col"`. A grid header uses `role="columnheader"`
and has no `scope` attribute, so the shared table carries the role instead.
The plain tables keep `scope="col"` under CPL-D3.

## Finding: cell functions and remount

`flexRender` creates a React element when the cell value is a function. A
column array built inside a page render makes a new component type on every
render, so React remounts each cell. The keys page rebuilt its columns on
every budget query update, which broke focus return after the delete dialog
closed. Every consumer now defines its columns at module level and passes the
page callbacks through the table `meta` option.

## Counts

| Measure | Before | After |
| --- | --- | --- |
| Tables through the shared component | 0 | 5 |
| Tables with a loaded-count footer | 0 | 3 |
| Test files / tests | 42 / 318 | 43 / 326 |
| Entry chunk gzip | 118.49 kB | 118.62 kB |
| Verifier | 23 passed, 25 failed | 26 passed, 22 failed |

## Fail-before

The new test file ran in a worktree at `a7f904b` with the test copied in.
The run failed with `Failed to resolve import "./DataTable"`. The verifier at
that commit reported V24, V25, and V26 red.

## Tests added

| Test | Assertion |
| --- | --- |
| Sort toggle | The Name header cycles none, ascending, descending, none. A display column has no `aria-sort`. |
| Keyboard sort | The header button takes focus, and one activation sorts the numeric column descending. |
| Row activation | Enter and Space activate the focused row. A click on an inner link does not. |
| Virtualized count | 150 rows report `aria-rowcount` 151 and render fewer rows than the data. |
| Selection column | Select all reports every id, and a selected row carries `aria-selected`. |
| Empty message and footer | The footer reads `100 records loaded · 100 per request` with a load control, and the bounded form states that more exist. |

The models tests still pass with `aria-rowcount` at 423 for 422 rows.

## Browser reading

Every page ran on the dev server at a 1,440 px viewport.

| Page | Observation |
| --- | --- |
| `/models` | Sticky header, six resize handles, Model ascending by default, and no horizontal scroll. Context sorts descending first. Enter on a focused row opens the model detail route. |
| `/keys` | Eight columns fit the panel width. The row holds copy, edit, disable, and delete controls. |
| `/providers/openai` | Served models shows Circuit and Routing filters and `117 of 117`. |
| `/audit`, `/presets`, `/files`, `/documents` | Each page shows its empty state on a fresh gateway. |

## Commands

| Command | Result |
| --- | --- |
| `pnpm -C console check` | Build, typecheck, and 326 tests pass. |
| `scripts/verify-console-polish.sh` | 26 passed, 22 failed. V24, V25, and V26 pass. |
| All other `scripts/verify-*.sh` | Pass. |
