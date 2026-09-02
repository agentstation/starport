# CPL-D1 Formatting vocabulary with tests

Branch `codex/cpl-d1`. Base `f107a06` (main after CPL-C5).

## What changed

The console now has one spelling for a count, a price, a context window, and
a moment in time. Every number formatter fixes decimals and never emits
exponent notation. The table names each owner and its change.

| Owner | Change |
| --- | --- |
| `lib/format.ts` `formatUSD` | New shared dollar core. A whole dollar stands bare (`$1`). A cent value keeps two decimals plus any exact digit (`$0.22`, `$0.125`). A sub-cent value keeps three significant digits in full (`$0.000185`). |
| `formatNanoUSD`, `formatPricePerM`, `formatPricePerK`, `formatUnitPrice` | Each one scales its unit and hands the dollars to `formatUSD`. An absent or unparseable price stays `null` for the dash. |
| `formatPricePair` | New. Renders `$0.22 / M in · $0.88 / M out` in the DESIGN.md grammar. One unknown side shows the dash beside the known one. |
| `formatCount` | Exact below 10k, then one fixed decimal per step with a lowercase `k`, then `M`, then `B`. A value that rounds up moves to the next step, so 999,950 reads `1.0M`. |
| `formatContext` | Same steps, whole `k`, and a trimmed decimal on `M` and `B` (`128k`, `1M`, `1.5M`). |
| `utcTooltip` | Moved from two route copies into `format.ts`. It shares the zero-time guard with `formatRelativeTime`. |
| `components/ui/RelativeTime.tsx` | New `<time>` element with `whitespace-nowrap`, the UTC instant as `title` and `dateTime`, and the dash for an absent stamp. |
| `ChangesPanel.tsx` | An absent diff price renders the dash. The old code coerced it to `$0`. |
| Per-million spellings | `ModelsTable`, `ModelPicker`, `ModelDetail`, `ChangesPanel`, and the presets policy chips all say `/ M`. |

## Sites

The `RelativeTime` element replaced the relative phrase at 22 sites in 17
files. The table groups them.

| Group | Sites |
| --- | --- |
| Table cells | keys, members, teams, audit, presets (list and history), usage (all-time view), documents, incident log |
| Detail phrases | accounts, team detail, member detail, freshness bar, provider incident, provider credential, catalog changes header |
| Credential phrases | BYOK stored and last used, shared credential applied and last used |

Two `title` attributes with a raw stamp went away because the element
carries the instant. The usage row keeps its raw title only in the absolute
time view. The provider card tooltip and the incident summary build plain
strings, so they still call `formatRelativeTime`.

## Counts

| Measure | Before | After |
| --- | --- | --- |
| `utcTooltip` copies in routes | 2 | 0 |
| Per-million spellings | 4 | 1 |
| Table-driven format cases | 0 | 73 |
| Test files / tests | 41 / 242 | 42 / 318 |
| Entry chunk gzip | 118.47 kB | 118.49 kB |
| Verifier | 21 passed, 27 failed | 23 passed, 25 failed |

## Fail-before

The two new test files ran in a worktree at `f107a06` with the new tests
copied in.

| Result | Count |
| --- | --- |
| Failed | 46 |
| Passed | 30 |

The failures include the exponent sweep (`$1.0e-9` from `formatNanoUSD(1)`),
the `1000.0K` step trap, the uppercase `K`, the missing `formatPricePair`
export, and the missing `RelativeTime` module.

## Tests added

| File | Test |
| --- | --- |
| `lib/format.test.ts` | Nine `test.each` tables: count, context, USD, nano-USD, per-million, per-thousand, unit price, price pair, and UTC tooltip. |
| `lib/format.test.ts` | One sweep over 25 magnitudes from 1.5e-12 to 1.5e12 that asserts no output matches `e-` or `e+`. |
| `components/ui/RelativeTime.test.tsx` | The element is a `time` with `whitespace-nowrap`, the ISO instant as title and datetime, and a bare dash for a zero-value stamp. |

## Browser reading

The models table at `/models` on the dev server showed `$0.005 / M in` with
the dash on the output side for an embedding offering, and `131k` in the
context column. A regex for `\de-\d` over the page text found no match.

## Commands

| Command | Result |
| --- | --- |
| `pnpm -C console check` | Build, typecheck, and 318 tests pass. |
| `scripts/verify-console-polish.sh` | 23 passed, 25 failed. V22 and V23 pass. |
| All other `scripts/verify-*.sh` | Pass. |
| `grep -r "utcTooltip = " console/src/routes \| wc -l` | 0 |
