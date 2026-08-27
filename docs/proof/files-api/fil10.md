# FIL10, cleanup

FIL10 closes the files API plan. The plan file leaves the tree, the proof root
stays, and `docs/TASKS.md` becomes the record of what shipped.

## Every ledger row is terminal

The ledger held eleven rows, FIL0 through FIL10. Ten read `done` before this
task, and FIL10 is the last. No row reads `blocked`, `deferred`, or
`no-action`, so the outcome the plan owns is complete.

## The task list moves, and two rows move with it

The FIL row left Active Work and joined Recently Completed. Its note states the
seam split, the two scopes, and the retention floor and ceiling. It also states
the stored-byte bound, the console view, and the gate at its terminal count of
22.

Two rows moved in the other direction. The async media jobs plan and the
document parser plan both read `active`, and both sat under Proposed Work. A
plan that reads `active` in its own file and `proposed` in the task list gives
an agent two answers. Both rows now sit under Active Work, and each says the
files API plan closed rather than that it documented a seam.

The reranking plan stays under Proposed Work. It depends on no other plan.

## A deleted file leaves no link behind

Three references pointed at `files-api-plan.html`. Two sit in the async media
jobs plan and one in the document parser plan.

| File | What the reference said |
| --- | --- |
| `docs/plans/async-media-jobs-plan.html` | the files API plan owns the blob seam |
| `docs/plans/async-media-jobs-plan.html` | the resequencing that put this plan second |
| `docs/plans/document-parser-plan.html` | the files API plan owns stored bytes |

Each fact stays. Only the link goes, because the target is gone. The first and
third now name the proof root, so a reader who wants the reasoning has a path
that resolves.

`scripts/verify-doc-links.sh` passed before this change and after it. The
verifier reads Markdown links, and these three are HTML anchors between plan
files, so the check would not have caught them. The plan asked for no reference
to the deleted file, and `git grep` now returns none.

## Sixteen merged branches leave the remote

Every `codex/` branch on the remote belonged to a merged pull request. The
squash merges mean `git branch --merged` reports none of them. This task
therefore read each pull request state first, then deleted the branch.

The remote now holds `main` and the branch of this pull request.

## Acceptance

| Statement | Held by |
| --- | --- |
| the plan file is absent | `git rm docs/plans/files-api-plan.html` |
| the task list reports the outcome with its condition count | `docs/TASKS.md` |
| the link verifier passes with no reference to the deleted file | `git grep files-api-plan.html` returns nothing |
| the proof root stays | `docs/proof/files-api/` holds eleven files |

## Verification

`bash scripts/verify-doc-links.sh` reports `PASS documentation links`.
`bash scripts/test-doc-link-verifier.sh` reports
`PASS documentation link verifier edge cases`.
`bash scripts/verify-files-api.sh` reports `Summary: 22 passed, 0 failed`.
`bash scripts/verify-developer-experience.sh` reports `47 passed, 0 failed`.

The technical writing linter reports `0 diagnostic(s)` for `docs/TASKS.md` and
for this file.
