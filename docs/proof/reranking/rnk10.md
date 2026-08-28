# RNK10 proof: the documentation and the gate that runs

Task RNK10 of the reranking plan. Conditions RNK-V19 and RNK-V20.

## Problem

`scripts/verify-reranking.sh` held 22 conditions and no workflow ran it. A gate
that no workflow runs cannot report a regression, so every condition it holds is
a claim nobody checks. Reranking also had no page. An operator could read the
scope line in the guide and find nothing about the route, the request, or the
price owner.

## What landed

### The gate

The verifier joined two lists that have to agree. The CI list is in the "Verify
release contracts" step of the Release Contract job. The evidence list is the
`## Required evidence` block in `AGENTS.md`, which `CLAUDE.md` symlinks. Both now
name `scripts/verify-reranking.sh` between the document parser gate and the
catalog performance one.

`AGENTS.md` also states what the gate means and where it stops: 22 conditions,
`RNK-V01` through `RNK-V22`, terminal. That sentence is what keeps a later task
from quietly widening the count.

### The operator guide

A `## Reranking` section sits before `## Document Parsing`, because a rerank
scores documents and the parser reads them. It holds four things:

- The two routes and the `rerank:write` scope. The scope stands alone: a rerank
  request reads the documents the caller sent rather than a stored one.
- What each protocol does with the ranked text. The OpenAI path takes
  `return_documents`. The OpenRouter schema echoes on every result.
- The request shape, the result shape, and the document bound the gateway
  refuses against.
- Where the price comes from. Starmap owns the operation, the offerings, and
  the price. The section names the two billing bases. It also states that an
  offering with no price in the unit it bills loses the operation before
  planning.

### The README and the architecture

The README version 1 scope gained a reranking bullet naming both routes, the
scope, and the price owner.

`docs/ARCHITECTURE.md` gained `POST /v1/rerank` and `POST /api/v1/rerank` in the
route lists, and a paragraph on why `rerank:write` covers what neither
`chat:write` nor `files:read` does.

The operations table was three rows short of the shipped set. It states that it
holds what this build can plan, and `internal/routing/operations.go` names ten
operations to its seven. Two of them, `videos-generations` and
`documents-recognition`, landed with earlier plans and never reached the table.
All three rows are there now, so the sentence above the table is true again.

The video routes had the same gap and are now in both route lists.

## Verification

| Check | Result |
| --- | --- |
| `bash scripts/verify-reranking.sh` | 22 passed, 0 failed, terminal |
| `bash scripts/verify-doc-links.sh` | exit 0 |
| `bash scripts/verify-developer-experience.sh` | exit 0 |
| Twenty-one gates in the required-evidence list | all exit 0 |
| `technical-writing lint` on each changed document | no new diagnostics |

The plan's acceptance is met: the verifier is terminal, the CI workflow names
it, and the evidence list holds it with its count.
