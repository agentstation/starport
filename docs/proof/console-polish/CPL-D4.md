# CPL-D4 proof: charts with a series descriptor, an interval, a legend, and honesty

Branch `codex/cpl-d4`. Base: the CPL-D3 squash `e1ecafc`.

## What changed

| Owner | Change |
| --- | --- |
| `lib/usageBuckets.ts` | New. Owns the interval choice, the floored start, the daily calendar step, the oldest-record clamp, the nano-USD integer sums, the empty-slice latency, and the caption text. |
| `components/ui/Chart.tsx` | Owns the three neutral steps, the usage sync id, the dashed cursor, the endpoint locator, the reduced-motion hook, the chip legend, and the caption row. The plot fills the card through the Recharts 3 `responsive` prop. |
| `routes/usage.tsx` | The local bucket code moved out. Every chart carries the sync id, the endpoint dot, the caption, and the motion flag. The request stack reads one series descriptor. The spend area reads nano-USD integers on a 60 px axis. The token axis takes integer ticks. The latency line drops `connectNulls` and its per-point dots. |
| `lib/api.ts`, `lib/queries.ts` | The activity filter carries `until`. The overview sample bound is a named constant, and a second query reads the day before. |
| `components/overview/StatsRow.tsx` | Trends hide when the sample fills the bound, and the footer says why. Each of the three counted stats shows a delta against the day before. The error sparkline scales to the request peak. |
| `components/overview/Sparkline.tsx` | Draws a zero baseline before the series and accepts a shared maximum. |

## Interval by range

| Window | Interval | Tick and title format |
| --- | --- | --- |
| Last hour | 5 minutes | `M/D HH:mm` |
| Last 24 hours | 1 hour | `M/D HH:mm` |
| Last 7 days | 6 hours | `M/D HH:mm` |
| Last 30 days and all time | 1 day | `M/D` |

The daily step advances by calendar date, so a DST change keeps every bucket at local midnight.

## Honesty rules

A window with more pages behind it starts its buckets at the oldest loaded record. The caption then reads `Newest 1,000 requests only · 8/26 00:00 to 9/2 00:46 · 6h buckets`. The overview shows a delta only when the current sample and the prior sample both sit under the 500-record bound. A prior day with no requests gives no delta, because a rise from nothing has no rate.

## Counts

| Measure | Before | After |
| --- | --- | --- |
| Test files and tests | 44 / 332 | 46 / 339 |
| Entry chunk (gzip) | 118.66 kB | 118.67 kB |
| Verifier | 27 passed, 21 failed | 29 passed, 19 failed |

## Fail-before

At `e1ecafc` (origin/main) in a fresh worktree the verifier reported `FAIL CPL-V28` and `FAIL CPL-V29` with 27 passed and 21 failed. The two sparkline tests failed there. The bucketing file and the usage route test file did not resolve `@/lib/usageBuckets`.

## Tests added

| File | Test |
| --- | --- |
| `lib/usageBuckets.test.ts` | A thirty-day range yields 31 daily buckets floored to local midnight. The interval choice per window. A truncated sample starts at the oldest loaded record and captions itself. Spend sums as nano-USD integers, and an empty slice carries no latency. |
| `routes/listState.test.tsx` | A capped sample renders the truncation caption on all four charts. |
| `components/overview/Sparkline.test.tsx` | A flat series still draws its zero baseline. A shared maximum scales the series lower than its own. |

## Browser reading

Vite dev server at `127.0.0.1:5174`, viewport 1,440 px. The local gateway holds no traffic, so the page read a patched activity response of 180 records over 7 days.

| Page | Reading |
| --- | --- |
| `/usage?range=7d` | Four plots at 233 by 144 px. One endpoint dot per chart at radius 3 in Text 3. The request legend reads `ok`, `cancelled`, `error`. Every caption reads `8/26 00:00 to 9/2 00:46 · 6h buckets`. One hover opens all four tooltips on the same slice, titled `8/29 12:00 to 8/29 18:00`. |
| `/` | Three sparklines with a baseline each. Three deltas beside the headline values. |

A hidden tab pauses the Recharts animation frames, so the first reading showed the series at their first frame. A visible tab completes them.

## Commands

| Command | Result |
| --- | --- |
| `pnpm typecheck` | Clean. |
| `pnpm exec vitest run` | 46 files, 339 tests passed. |
| `pnpm build` | Entry chunk 118.67 kB gzip. |
| `bash scripts/verify-console-polish.sh` | 29 passed, 19 failed. V28 and V29 pass. |
| `bash scripts/verify-*.sh` (23 gates, release build gates excluded) | All 23 passed. |
