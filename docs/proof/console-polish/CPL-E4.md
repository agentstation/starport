# CPL-E4 proof: confirmations for every cascading write

Branch `codex/cpl-e4`. Base: the CPL-E3 squash `93dc1bc`.

## What changed

| Owner | Change |
| --- | --- |
| `console/src/components/ui/ConfirmDialog.tsx` | One shape for a write the operator cannot take back. It builds on the CPL-C2 dialog, states "There is no undo.", keeps Cancel first, and holds the gateway's refusal in the dialog error slot. `reasonOf` reads the message a failed write carries. |
| `console/src/components/teams/DeleteTeamModal.tsx` | Names the team, the member count, and the grant count from the roster and grants reads. Both lists go with the team on the gateway. |
| `console/src/components/accounts/DeleteAccountModal.tsx` | Names the account and the count of gateway API keys it holds. The gateway refuses the delete while a key remains, and the dialog shows that 409 message. |
| `console/src/routes/teams.tsx` and `accounts.tsx` | The trash buttons open the modals. The delete mutation left the routes and lives in each modal. |
| `console/src/components/teams/TeamDetailPanel.tsx` | Member removal and grant removal each open a confirmation that names the person or the account and the team. |
| `console/src/components/members/MemberDetailPanel.tsx` | A direct grant removal opens a confirmation that names the account and the member. |
| `console/src/components/accounts/AccountTemplatesPanel.tsx` | A template delete opens a confirmation that names the template and states that stamped accounts keep their settings. |
| `console/src/components/jobs/JobsPanel.tsx` | A job cancel opens a confirmation that names the job and the model. The dismiss button reads "Keep running". |
| `console/src/routes/presets.tsx` | The history dialog stages a restore inline. The step names the head revision, the revision it copies, and the revision the restore lands as. |
| `console/src/components/accounts/AccountPolicyPanel.tsx` | One "Save policy" button sends the BYOK rule and the provider access in one write. It stays disabled while either answer is invalid. |
| `console/src/routes/presets.tsx` (table) | The visual check found the Model and Overrides cells for an empty value printed the literal text `\u2014`. JSX text does not decode an escape, so both cells now render the dash from a string expression. |

## Amendment to the plan

The plan named a preset count for an account. A preset carries no account on the gateway, so the account dialog names the key count alone. The plan also named "There is no undo." for a restore. A restore lands as a new head revision and the old head stays in the history, so the restore step states that instead. It uses the primary button, not the destructive one.

## Words on each dialog

| Write | Dialog words |
| --- | --- |
| Team delete | "Delete Platform? Its 3 members leave the roster, and its 1 account grant end with the team. Keys attributed to the team spend without a team ceiling from the next request on." |
| Account delete | "Delete acme? It holds 2 gateway API keys. The gateway refuses the delete while any key remains, so delete or reassign them first." |
| Member removal | "Remove Ada from Platform? They lose every account the team grants from the next request on. A grant made to them directly stays." |
| Grant removal | "Remove the acct grant from Platform? Members reach acct only through another grant from the next request on." |
| Template delete | "Delete the trial template? Accounts already stamped from it keep their settings. No new account can start from it." |
| Job cancel | "Cancel job-1 on mock/video-1? The job stays in the list as cancelled and never produces an asset." |
| Preset restore | "Restore @preset/draft from revision 5 to revision 3? The restore lands as revision 6, and revision 5 stays in the history." |

While a count is still in flight, the team dialog says "Every member" and "every account grant" instead of a number it has not read.

## Counts

| Measure | Before | After |
| --- | --- | --- |
| Console tests | 350 | 356 |
| Entry chunk, gzip | 118.71 kB | 118.69 kB |
| Verifier | 34 passed, 14 failed | 35 passed, 13 failed |

## Fail-before

At `origin/main` (`bbf5321`) with the E4 test files copied in, V35 was red and the verifier reported 32 passed, 16 failed. The 16 E4 tests failed there. They are the four route tests, the jobs cancel test, the three confirm-through-dialog panel tests, the template delete test, and the seven single-Save policy tests. Five E3 budget tests also failed at that base because E3 was not yet merged.

## Tests added

| File | Test |
| --- | --- |
| `console/src/routes/teams.test.tsx` | "deletes a team only after the dialog names what goes with it" asserts no DELETE travels before the confirm, and that the dialog names 3 members and 1 grant. "keeps the team when the operator cancels the dialog" asserts Cancel sends nothing. |
| `console/src/routes/accounts.test.tsx` | "names the keys an account holds and keeps the refusal in the dialog" asserts the key count and that the 409 message stays in the open dialog. |
| `console/src/routes/presets.test.tsx` | "names both revisions before a restore travels" asserts "from revision 5 to revision 3" and the rollback body `{to_revision: 3, revision: 5}`. "renders a dash, not an escape, for an empty overrides cell" fails on the escaped text and passes on the fix. |
| `console/src/components/jobs/JobsPanel.test.tsx` | "cancels a running job only after the operator confirms". |
| Panel tests | The member, grant, and template tests now confirm through the dialog and assert the write did not travel before it. The policy tests expect one body with both fields. |

## Commands

| Command | Result |
| --- | --- |
| `pnpm typecheck` | Clean. |
| `pnpm exec vitest run` | 51 files, 356 tests passed. |
| `pnpm build` | Built. Entry chunk 118.69 kB gzip. |
| `bash scripts/verify-console-polish.sh` | 35 passed, 13 failed. V35 passes. |
| `bash scripts/verify-*.sh` (23 gates, release build gates excluded) | All 23 passed. |

## Visual check

The check ran on the vite dev server at `127.0.0.1:5174` against a rebuilt dev gateway on port 8080.

| Step | Result |
| --- | --- |
| Account delete | A throwaway account `polish-e4` opened the "Delete account" dialog. It named the account in bold, stated "It holds no gateway API keys." and "There is no undo.", and put Cancel before the red "Delete account" button. Confirming removed the row. |
| Preset restore | A throwaway preset `polish-e4` with two revisions opened "History of @preset/polish-e4". "Restore revision 1" staged the inline step: "Restore @preset/polish-e4 from revision 2 to revision 1? The restore lands as revision 3, and revision 2 stays in the history." with "Keep current" and the primary "Restore to revision 1". "Keep current" cleared the step. |
| Presets table | The empty Overrides cell printed the literal text `\u2014` while the Routing cell showed a dash. The table fix above corrects both escaped cells. |

Both throwaway records left the dev gateway after the check.
