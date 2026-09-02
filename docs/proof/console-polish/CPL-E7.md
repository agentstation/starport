# CPL-E7 proof: batches panel on the jobs page

Branch `codex/cpl-e7`. Base: the CPL-E6 squash `bf189d4`.

## What changed

| Owner | Change |
| --- | --- |
| `console/src/lib/api.ts` | `Batch`, `BatchRequestCounts`, `BatchList`, and `TERMINAL_BATCH_STATES` name the OpenAI batch object. `listBatches` reads `GET /v1/batches`. `cancelBatch` posts the cancel route. `fetchStoredFile` reads one stored file's bytes. `fetchJobAsset` and `fetchStoredFile` share one `fetchBytes` helper. |
| `console/src/lib/queries.ts` | `queries.batches()` owns the listing key. |
| `console/src/components/jobs/BatchesPanel.tsx` | New. The panel lists batches with a state pill, a completed and failed and total bar, the creation time, and the output and error file downloads. A cancel travels only from a confirmation dialog. The panel polls every five seconds while a batch is not terminal. |
| `console/src/routes/jobs.tsx` | The page gains a tab row. The `tab` search parameter opens the batches panel. The plain link opens on video jobs. The video panel reads "Video jobs". The loader prefetches the listing of the open tab. |

## Design notes

The bar is one meter with two fills. Completed lines fill in success green from the left. Failed lines follow in error red. The unfilled remainder is what has not run yet. The counts beside the bar carry the same three numbers, because a bar alone is not a number a reader can quote.

A batch file is a stored file. The output and error downloads fetch the bytes under the held credential and hand the browser a blob. The video player fetches its asset the same way, because a plain link sends no Authorization header.

The batch listing route serves only the caller's account, so the panel has no account picker. The console session runs as the canonical account on a machine-local deployment.

## Counts

| Measure | Before | After |
| --- | --- | --- |
| Console tests | 365 | 370 |
| Entry chunk, gzip | 118.74 kB | 118.80 kB |
| Verifier | 39 passed, 9 failed | 40 passed, 8 failed |

## Fail-before

I ran the check at `origin/main` (`07d5a76`, the CPL-E5 squash, before PR #350 merged) with the two new test files copied in. V40 was red, and the verifier reported 36 passed, 12 failed. Every test in `console/src/components/jobs/BatchesPanel.test.tsx` and `console/src/routes/jobs.test.tsx` failed there, because the panel module did not exist and the page had no tab row.

## Tests added

| File | Test |
| --- | --- |
| `console/src/components/jobs/BatchesPanel.test.tsx` | "a batch row carries the completed, failed, and total counts on its bar" asserts the bar text, the meter values, and the state pill. "a failed batch names the reason and offers its error file" asserts the reason text, the absent cancel control, the file fetch, and the blob handoff. "cancels a running batch only after the operator confirms" asserts the dialog and the cancel call. |
| `console/src/routes/jobs.test.tsx` | "opens the batches panel from the tab search parameter" asserts the batch row and the selected tab. "opens on video jobs by default" asserts the default tab and the empty video state. |

## Commands

| Command | Result |
| --- | --- |
| `pnpm typecheck` | Clean. |
| `pnpm exec vitest run` | 57 files, 370 tests passed. |
| `pnpm build` | Built. Entry chunk 118.80 kB gzip. |
| `bash scripts/verify-console-polish.sh` | 40 passed, 8 failed. V40 passes. |
| `bash scripts/verify-async-media-jobs.sh` | 18 passed, 0 failed. |
| `bash scripts/verify-*.sh` (23 gates, release build gates excluded) | All 23 passed. |

## Visual check

I opened `/jobs?tab=batches` on the dev gateway. The empty state names the upload purpose and the submit route. I uploaded a three-line JSONL file with purpose `batch` and submitted it to `POST /v1/batches` from the page. The batch ran to `completed` with zero completed lines and three failed lines, because the dev gateway holds no provider credential. The row shows the green pill, the red bar, the text "0 of 3 completed · 3 failed", "just now", and the Error file control. A fetch of the error file route from the page returned status 200 and a first line that names `line-2` with status code 401.

UNVERIFIED: a browser download of the error file. A download needs the user's permission, so the test asserts the blob handoff instead. UNVERIFIED: a live cancel. The dev gateway ran the batch to its end within the poll interval, so no non-terminal row was on screen. The dialog and the cancel call hold unit-test evidence only.
