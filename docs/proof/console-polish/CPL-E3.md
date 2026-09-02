# CPL-E3 proof: budgets as meters on teams and accounts

Branch `codex/cpl-e3`. Base: the CPL-E2 squash `bbf5321`.

## What changed

| Owner | Change |
| --- | --- |
| `internal/server/controllers/budgets.go` | One owner for the budget meter shape. `budgetMeter` reads the window totals for a scope and answers the six-key entry, or the unavailable entry when the usage store is absent or fails. `limitBudgets` folds the spend and token budgets of a `limits.Limits` carrier into one block. |
| `internal/server/controllers/members.go` | Team reads carry `revision` and a `budgets.spend` meter when the team holds a budget. `PUT /api/v1/admin/teams/{team_id}` accepts an optional `revision`. A mismatch answers 409 before validation. An absent revision keeps the unconditional update. |
| `internal/server/controllers/accounts.go` | Account list, get, create, and update carry `budgets.spend` and `budgets.tokens` when the effective limits set one. |
| `internal/server/controllers/admin.go` | The key detail budget block uses the same six keys as the team and account meters. |
| `internal/server/controllers/controllers.go` | The composition root hands the usage repository to both controllers. |
| `console/src/components/ui/BudgetLine.tsx` | The meter left `keys.tsx`. It renders a `meter` role with the used, limit, and percent values, and it names an unreadable meter in words. The track is a `border-3` overlay, so it stays visible on the raised sheet ground where the old `bg-raised` track vanished. |
| `console/src/components/teams/TeamDetailPanel.tsx` | The panel draws the team meter, sends the revision it read with each save, refuses an emptied amount, and removes a budget only through a confirmed dialog. |
| `console/src/components/accounts/ProviderSpendPanel.tsx` | The account detail reads `/api/v1/accounts/{id}/usage/providers` and lists spend by provider with a truncation note and a no-price note. |
| `console/src/routes/accounts.tsx` | The detail and the table draw the account meters under the limit chips, and the detail mounts the provider spend panel. |
| `console/src/lib/format.ts` | `formatRelativeTime` names a future instant as `in 17h`. Before this change a future instant read `just now`, and the budget reset time did too. |
| `console/src/lib/api.ts` and `queries.ts` | `BudgetMeters`, `ProviderUsage`, and the team `revision` gain types. `updateTeam` sends the revision. `queries.accountProviderUsage` owns the new query. |

## Amendment to the plan shape

The plan named the meter keys `limit`, `interval`, `spend`, `remaining`, and `window_start` in integer USD cents. The gateway meters every spend value in integer nano-USD, and the key detail budget already answered `used` in that unit. One shape across keys, teams, and accounts serves the console better than a second unit. The entry now carries six keys.

| Key | Meaning |
| --- | --- |
| `limit` | The budget ceiling in nano-USD, or in tokens for a token budget. |
| `interval` | `day`, `week`, or `month`. |
| `used` | The window total in the same unit. |
| `remaining` | `limit` less `used`, never below zero. |
| `window_start` | The window start as RFC 3339. |
| `window_end` | The window end as RFC 3339. The console names it as the reset time. |

An unreadable meter carries `limit`, `interval`, both window bounds, and `error: "unavailable"`.

## Honesty rules

| Fact | Words on the page |
| --- | --- |
| Usage store absent or failed | `usage unavailable` beside the limit, and no bar |
| Budget exhausted | `spend exhausted`, and the bar reads as an error |
| More than 80 percent used | The bar reads as a warning |
| Provider rollup truncated | `The rollup stopped at its record bound, so these rows understate the window.` |
| Requests without a price | `N requests without a price are not in the spend.` |
| No provider reached | `No request reached a provider for this account in the window.` |
| Admin plane refused | `Reading provider spend needs an admin-scoped key.` |

## Counts

| Measure | Before | After |
| --- | --- | --- |
| Console tests | 342 | 350 |
| Entry chunk | 118.68 kB gzip | 118.71 kB gzip |
| Verifier | 32 passed, 16 failed | 34 passed, 14 failed |

## Fail-before

At `bbf5321` (origin/main) in a fresh worktree the verifier reported `FAIL CPL-V33` and `FAIL CPL-V34` with 32 passed and 16 failed. The copied `budgets_test.go` did not compile there, because the six field constants and the controller usage field did not exist. The five new team panel tests failed there, and the provider spend test could not resolve its component.

## Tests added

| File | Test |
| --- | --- |
| `internal/server/controllers/budgets_test.go` | `TestTeamReadIncludesBudgetUsage` asserts the six keys, the used and remaining values, and the absent block on an unmetered team. `TestTeamBudgetWithoutUsageStoreReadsUnavailable` asserts the unavailable entry. `TestTeamUpdateRejectsStaleRevision` asserts the 409, the surviving rename, the fresh revision, and the unconditional update. `TestAccountReadIncludesBudgetUsage` asserts both account meters on the get and the list. |
| `console/src/components/teams/TeamDetailPanel.test.tsx` | Five tests: the refused empty amount, the confirmed removal with its revision, the revision on a save, the meter at 80 percent, and the unreadable meter. |
| `console/src/components/accounts/ProviderSpendPanel.test.tsx` | Three tests: the sorted rows with both notes, the empty sentence, and the locked line. |
| `console/src/lib/format.test.ts` | One test: a future stamp reads `in 15h` or `in 3d`, and one under a minute ahead reads `just now`. |

## Commands

| Command | Result |
| --- | --- |
| `gofmt -l internal/server/controllers/` and `go vet ./...` | Clean. |
| `go test ./internal/server/... ./internal/limits/... ./internal/usage/... -count=1` | All packages ok. |
| `make lint` | 0 issues. |
| `pnpm typecheck` | Clean. |
| `pnpm exec vitest run` | 48 files, 350 tests passed. |
| `pnpm build` | Built. Entry chunk 118.71 kB gzip. |
| `bash scripts/verify-console-polish.sh` | 34 passed, 14 failed. V33 and V34 pass. |
| `bash scripts/verify-*.sh` (23 gates, release build gates excluded) | All 23 passed. |

## Visual check

The account panel rendered against a rebuilt local gateway on the vite dev server. The gateway runs without an identity provider, so the Teams page reports that identity is not configured. The team meter has only its tests as evidence.

The check found two defects that the tests had not caught. A five-dollar daily budget set through the admin route answered the six-key meter. The panel drew `$5 left · resets just now`, because the relative formatter had no future phrase. The meter track used the sheet's own ground color, so an empty bar left no trace. After the two fixes the row reads `$5 left · resets in 17h` over a visible track. The provider spend panel read `No request reached a provider for this account in the window.` with its window caption.
