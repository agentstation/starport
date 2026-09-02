# CPL-D3 proof: plain table and label pass

Branch `codex/cpl-d3`. Base: the CPL-D2 tip `ca6c3f6`.

## What changed

| Owner | Change |
| --- | --- |
| `components/ui/Pill.tsx` | New lifecycle pill: tint background, solid text, full radius, 12 px medium, five tones. |
| `components/files/FilesPanel.tsx` | Column scopes, `px-4 py-2.5` cells, right-aligned Size, status as a pill, neutral progress fill. |
| `components/jobs/JobsPanel.tsx` | Column scopes, `px-4 py-2.5` cells, right-aligned Elapsed, state as a pill with the five job states mapped to tones. |
| `components/documents/DocumentsPanel.tsx` | Column scopes on both tables, `py-2.5` cells, right-aligned Pages, Took, and Per 1K pages. |
| `components/models/ModelDetail.tsx` | Header row moved from Text 4 to Text 3 at weight 500, column scopes, `px-4 py-2.5` cells, seven numeric columns right-aligned, labeled chip rows with `role="group"`. |
| `components/models/ChangesPanel.tsx` | Column scopes, sentence-case headers, `py-2.5` cells. |
| `routes/accounts.tsx`, `routes/teams.tsx` | Column scopes, `py-2.5` cells, right-aligned Keys and Spend budget. |
| `routes/keys.tsx` | The key status pill uses the shared pill. |
| Eight acceptance files | Every `text-text-4` moved to `text-text-3`. |
| Provider, credential, model, author, preset, and incident headings | Uppercase micro-labels rewritten as sentence-case 13 px Text 2 section titles. |
| `routes/models_.$modelId.tsx` | The tags chip row carries a label and `role="group"`. |
| `ServedCredentialPanel.tsx`, `FilesPanel.tsx` | Progress fills moved from the accent link color to Text 3. |

The ChangesPanel price table keeps its horizontal padding. It sits inside a panel that already carries the page gutter, so `px-4` would indent it twice.

## Card radius

The card radius was already 8 px. `Card.tsx` uses `rounded-md`, and `tokens.css` sets `--radius-md: 8px`. The browser reading below confirms it. The task needed no edit.

## Counts

| Measure | Before | After |
| --- | --- | --- |
| `text-text-4` in the eight acceptance files | 27 | 0 |
| `scope="col"` across the seven plain tables | 0 | 47 |
| Uppercase section headings on the provider, credential, model, author, and preset pages | 11 | 0 |
| Test files and tests | 43 / 326 | 44 / 332 |
| Entry chunk (gzip) | 118.62 kB | 118.66 kB |
| Verifier | 26 passed, 22 failed | 27 passed, 21 failed |

## Fail-before

At `a7f904b` (origin/main) in a fresh worktree the verifier reported `FAIL CPL-V27` with 23 passed and 25 failed. The four new tests failed there, and `Pill.test.tsx` did not resolve `./Pill`.

## Tests added

| File | Test |
| --- | --- |
| `components/ui/Pill.test.tsx` | A pill carries the tone tint, a full radius, and the label. A neutral pill carries no error color. |
| `components/files/FilesPanel.test.tsx` | Status renders as a pill. All seven headers declare `scope="col"`. Size aligns right in the header and the cell. |
| `components/jobs/JobsPanel.test.tsx` | A running job renders an info pill that reads "in progress" with no underscore. |
| `components/models/ModelDetail.test.tsx` | The offering table declares eleven column scopes and right-aligns the seven numeric columns. Each capability tier is a labeled group. |

## Browser reading

Vite dev server at `127.0.0.1:5174`, viewport 1,440 px.

| Page | Reading |
| --- | --- |
| `/models/openai%2Fgpt-5.4` | 11 headers, 11 with `scope="col"`, 7 right-aligned. Header and cell padding `10px 16px`. Row height 40.5 px. Header color Text 3. Four labeled groups. The Providers heading renders 13 px with no text transform. Card radius 8 px. |
| `/keys` | Three status pills at 9999 px radius. |
| `/providers/openai` | Six section headings, none uppercase. |
| `/files`, `/jobs` | The local gateway holds no files and no jobs, so the pills render only in the tests. |

## Commands

| Command | Result |
| --- | --- |
| `pnpm typecheck` | Clean. |
| `pnpm exec vitest run` | 44 files, 332 tests passed. |
| `pnpm build` | Entry chunk 118.66 kB gzip. |
| `bash scripts/verify-console-polish.sh` | 27 passed, 21 failed. V27 passes. |
| `bash scripts/verify-*.sh` (23 gates, release build gates excluded) | All 23 passed. |
