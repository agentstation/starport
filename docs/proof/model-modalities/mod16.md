# MOD16 the gate runs and the vocabulary has a home

A gate that no workflow runs cannot report a regression. MOD16 puts the media
gate in CI, names it in the required evidence list, and writes down two
vocabularies. The media surface rests on both.

## The gate reaches CI

The media gate now runs in the *Verify release contracts* step, beside the
other durable gates. It reports 26 of 26 conditions.

The last two conditions ask about the gate itself:

- MMD-V25 asks whether a workflow runs it.
- MMD-V26 asks whether the required evidence list names it and its count.

Both now hold.

## The evidence list names the gate and the split

The pre-pull-request block in `AGENTS.md` gains the command. A paragraph beside
it states what the gate guards and where it stops.

A second paragraph records decision MOD-D5. The media gate owns the media
surface alone.

The OpenRouter parity gate therefore keeps its terminal count of 16 and its own
stated meaning. A new media route does not move it.

## Where the vocabulary lives

Two vocabularies decide where a media request goes. Neither had a written home
outside the Go source.

A modality names one payload family. The architecture document now tables the
five that `internal/inference` defines. It names `internal/catalog` as the
owner of the translation from the Starmap spelling.

An operation names one provider inference call. The same section tables the
seven that `internal/routing` can plan. It also states what the planner does
with a catalog fact that names an eighth.

## The API surface names the media paths

The route blocks listed chat, embeddings, and models. They now list the five
OpenAI media paths and the three OpenRouter ones.

OpenRouter publishes no image edit path and no translation path. The section
says so, because a shorter list reads as an oversight without the reason.

## The files API plan is active

The files API plan moves from `proposed` to `active`. Its promotion gate asked
for 26 of 26 conditions here, and for owner approval of its five decisions.
Both hold.

Two more gate items name work that FIL0 owns: the OpenAI file object fields,
and the object store this deployment can reach. FIL0 closes both before FIL1
starts.

## Acceptance

| Condition | Evidence |
| --- | --- |
| MMD-V25 | the CI verification step runs the gate |
| MMD-V26 | `AGENTS.md` names the gate and its terminal count |

## Fail-before

Not applicable. The condition count is the measurement. The gate reported 24
passed and 2 failed before this task. It reports 26 passed and 0 failed after
it.

## Verification

- The media gate reports 26 passed and 0 failed.
- The documentation link check passes.
- Every other command in the required evidence list passes.
