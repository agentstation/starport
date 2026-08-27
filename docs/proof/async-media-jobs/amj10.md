# AMJ10 Cleanup

## Outcome

`docs/plans/async-media-jobs-plan.html` is gone. `docs/proof/async-media-jobs/`
stays. The `docs/TASKS.md` entry moved from Active Work to Recently Completed
and names the durable gate.

## The plan merged

AMJ9 merged as pull request #247. Every ledger row from AMJ0 through AMJ9 read
`done` before this task started. No row carried unfinished work into the
deletion.

The gate is what survives the plan. `scripts/verify-async-media-jobs.sh` is
terminal at 18 conditions and runs in CI. A later change that breaks a job
route therefore fails a check rather than contradicts a file nobody reads.

## What the TASKS entry has to carry

A plan file states the reasoning. Deleting it puts that burden on one table
row. The Recently Completed entry therefore names the decisions a reader would
otherwise reconstruct from the diff.

Six of them matter. The provider job identifier never reaches a caller, so a
deployment moves a model between providers without breaking a poll in progress.
Two bounds hold polling: a doubling backoff and a one-hour lifetime. Accounting
draws once at the terminal state rather than once per poll.

A stored asset has a stated 24-hour window and answers HTTP 410 past it. The
outstanding job bound is a level rather than a rate. All five video routes sit
behind one scope, because only the submitting account can read its own job.

## The remaining mentions of the deleted file name

`git grep -l async-media-jobs-plan.html` returns
`docs/proof/files-api/fil10.md`. Two rows in a table there record which files
held a reference to the files API plan when FIL10 removed those references.

Both stay. They are a record of past state, not a link to a live target, and
`bash scripts/verify-doc-links.sh` passes. The same precedent already exists in
the tree: `fil10.md` names `files-api-plan.html` in its own prose, and FIL10
accepted that while deleting the file.

Rewriting a merged proof record to erase a true statement about what the tree
once held would cost more than it buys.

## Evidence

```
git grep -l async-media-jobs-plan.html   only docs/proof/files-api/fil10.md
bash scripts/verify-doc-links.sh         PASS documentation links
bash scripts/verify-async-media-jobs.sh  Summary: 18 passed, 0 failed
docs/proof/async-media-jobs/             amj0.md through amj10.md, kept
```

Deleted branches: `codex/async-media-jobs-amj3` on the remote, and `amj3`,
`amj6`, `amj7`, `amj8`, and `amj9` locally. Each of the merged ones landed as a
squash commit on `main`.
