# RNK11 Cleanup

## Outcome

`docs/plans/reranking-plan.html` is gone, and it was the last file in that
directory, so `docs/plans/` is gone with it. `docs/proof/reranking/` stays. The
`docs/TASKS.md` entry moved from Active Work to Recently Completed and names the
durable gate.

## The plan merged

RNK10 merged as pull request #270. Every ledger row from RNK0 through RNK10 read
`done` before this task started. No row carried unfinished work into the
deletion.

The gate is what survives the plan. `scripts/verify-reranking.sh` is terminal at
22 conditions and runs in CI. A later change that breaks a rerank route
therefore fails a check rather than contradicts a file nobody reads.

## The empty directory

Four plans closed before this one, and each cleanup deleted its own file. This
one empties the directory. `docs/README.md` states that a plan lives under
`docs/plans/` while its campaign runs, and that a cleanup deletes it when the
campaign closes. That sentence stays true of a directory with nothing in it, and the next plan
recreates the path. No document links into `docs/plans/` any more, so
`bash scripts/verify-doc-links.sh` has nothing to resolve there.

## What the TASKS entry has to carry

A plan file states the reasoning. Deleting it puts that burden on one table row.
The Recently Completed entry therefore names the decisions a reader would
otherwise reconstruct from the diff.

Seven of them matter. Two billing bases exist rather than one, and the basis
decides which price every consumer reads. The plan defers Jina, because it publishes
no first-party price. The scope stands alone, because a rerank reads the
caller's own documents and writes no message.

Planning is operation-aware, so the planner refuses a rerank request to a chat
model before the provider call. The meter reads search units, because a Cohere turn
reports no tokens at all. An offering priced in no unit it bills records
`no_pricing` rather than zero. The spend bound refuses against the generation's
cheapest search unit, because the gateway knows that floor before it spends the
money.

## The remaining mentions of the deleted file name

`git grep -l reranking-plan.html` returns nothing. No other proof record names
this plan. The files API and async media jobs cleanups each had one, so each had
to weigh a past-state reference against a live link. This one does not.

## Evidence

```
git grep -l reranking-plan.html         no match
ls docs/plans                           no such directory
bash scripts/verify-doc-links.sh        PASS documentation links
bash scripts/test-doc-link-verifier.sh  PASS
bash scripts/verify-reranking.sh        Summary: 22 passed, 0 failed
Twenty-one gates in the required-evidence list   all exit 0
docs/proof/reranking/                   rnk0.md through rnk11.md, kept
```
