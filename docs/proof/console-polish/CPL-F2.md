# CPL-F2 proof: catalog and overview polish

Branch `codex/cpl-f2`. Base: the CPL-F1 branch tip, rebased onto the CPL-F1 squash before merge.

## What changed

| Owner | Change |
| --- | --- |
| `console/src/components/overview/ProvidersCard.tsx` | The card names each provider that holds a credential the gateway cannot use, with the reason in words and a link to the provider page. "Credentialed" counts only providers that report a credential, so "Credentialed" minus "Usable" equals the rows in that list. |
| `console/src/components/overview/QuickstartCard.tsx` | The card, the tab panel, and the snippet frame carry `min-w-0`, so the snippet scrolls inside its frame instead of pushing the card past its column. |
| `console/src/components/overview/StatsRow.tsx` | The footer caption "Per-request detail arrives with the usage page" is gone. The footer renders only when the sample is capped. |
| `console/src/components/models/ModelsTable.tsx` | The price column is 240 wide with a 200 minimum, so the output side of the price pair no longer clips. |
| `console/src/components/models/ModelDetail.tsx` | The offering table shows one "Price / M" column with the console-wide price pair in place of separate prompt and completion columns. |
| `console/src/components/providers/ProviderDetail.tsx` | The routing chip reads the state alone. The Reason column says in words why the planner dropped an unroutable offering, through `routingExplanation` and `offeringReason`. |
| `console/src/components/models/ChangesPanel.tsx` | A diff with nothing in it reads "No changes since the previous generation." with a detail line. A diff without history reads "Nothing to compare yet." with the server reason as a sentence. |
| `console/src/components/models/FreshnessBar.tsx` | `manifestSentence` leads with the effect of a missing manifest and then states the server reason as a sentence. The badge title and the details row both read it. |
| `internal/catalog/freshness.go` | The two reasons the console renders now read as clauses that a sentence can carry. |

## Design notes

The plan copy for the empty diff was "No changes since your last visit". The panel compares the last two accepted generations, not the reader's visits, so the copy names the previous generation instead. The proof records the deviation here.

The routing explanation lives in the Reason column rather than beside the chip. The column is flexible and truncates with a title, so the sentence reads in full at the default width. The chip column stays narrow, and a reader who filters by routing state still sees the same chip vocabulary.

The reason codes come from `internal/app/provider_reconciliation.go`, which maps the five planner exclusions onto the provider state vocabulary. A code the console predates falls back to the code with its underscores replaced, so a new filter still reads as a reason.

## Counts

| Measure | Before | After |
| --- | --- | --- |
| Console tests | 372 | 379 |
| Entry chunk, gzip | 118.81 kB | 118.81 kB |
| Verifier | 41 passed, 7 failed | 41 passed, 7 failed |

CPL-F2 has no verifier condition. The two acceptance tests below carry its evidence.

## Fail-before

I ran the two new test files at `origin/main` (`452a3e7`, the CPL-E7 squash) in a worktree. Four tests failed there: the two provider card tests that assert the reason line and the two changes panel tests that assert the empty copy. The two tests that assert an absence passed at the baseline, as expected.

## Tests added

| File | Test |
| --- | --- |
| `console/src/components/overview/ProvidersCard.test.tsx` | "a credentialed provider the gateway cannot use is named with its reason" asserts one row for the invalid credential, its reason in words, its link, and the three counts. "the reason list stays absent when every credential is usable" asserts no list. "credentialBlocked falls back to the state when the reason is absent" asserts the sort and the fallback. |
| `console/src/components/models/ChangesPanel.test.tsx` | "a diff with nothing in it says so instead of rendering an empty panel" asserts the lead and the detail. "a semantically equal diff reads the same lead and names the metadata" asserts the lead and the metadata detail. "a diff with content lists it and shows no empty state" asserts the negative. "a diff without history leads with the answer and reads the reason as a sentence" asserts the no-history copy. |
| `console/src/components/models/ModelDetail.test.tsx` | The offering table test now expects ten headers and the price pair string. |
| `console/src/components/providers/ProviderDetail.test.tsx` | The unroutable test now expects the bare chip and the explanation sentence. |

## Commands

| Command | Result |
| --- | --- |
| `pnpm typecheck` | Clean. |
| `pnpm exec vitest run` | 59 files, 379 tests passed. |
| `pnpm build` | Built. Entry chunk 118.81 kB gzip. |
| `bash scripts/verify-console-polish.sh` | 41 passed, 7 failed. |
| `gofmt -l internal/catalog/freshness.go` and `go vet ./internal/catalog/` | Clean. |
| `go test ./internal/catalog/` | ok. |
| `bash scripts/verify-*.sh` (23 gates, release build gates excluded) | 23 passed, 0 failed. |

## Visual check

I opened the console through the vite server on port 5174 against the dev gateway. The overview shows the provider counts 17 known, 5 credentialed, and 4 usable. Below the counts, one row names `google-vertex` with the reason `credential source unavailable`. The stats row has no footer caption. The curl snippet scrolls inside its frame. At a 1440 pixel viewport, the card's right edge matches the grid's right edge.

The models table shows the price pair in full for the embedding models, with the output side visible. On the Fireworks AI provider page, the served models table shows the `flux-1-schnell-fp8` row with the chip "unroutable". Its Reason column reads "The offering serves no operation this gateway routes." The "no manifest" badge title leads with the effect of the missing manifest.

UNVERIFIED: the two reason strings from `internal/catalog/freshness.go` in the live console. The dev gateway ran a binary built before the change, so the panel and the badge showed the old server strings inside the new sentences. The Go package tests pass, and the console tests assert the sentence shape with a fixed reason.
