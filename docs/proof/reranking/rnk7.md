# RNK7 proof: rerank routing

Date: 2026-08-28. Branch: `codex/reranking-rnk7`.

## What landed

A rerank request now reaches a rerank offering or nothing. The planner already
filters candidates by operation. Two gaps remained. The first gap is what the
caller hears when that filter empties the plan. The second gap is what the
chosen route carries onward.

## The refusal carries a type

`routing.ErrOperationUnsupported` joins `ErrModalityUnsupported` beside
`ErrNoCandidate`. The planner raises it when the plan holds no attempt and a
`missing_operation` rejection states why. The message names the route and the
operation, because those are the two things a caller can change.

The planner reads the operation before the modality. The operation is the
coarser mismatch: a model that serves other operations cannot answer the
request under any modality, capability, or price the caller changes.

Three seams carry it. In `internal/router`, `routePlanFailure` passes it
through the collapse onto `ErrNoModelsAvailable`. `routeOperation` now calls
that function rather than repeating the collapse. In `internal/proxy`,
`routeFailure` turns it into a `ValidationError` on the `model` field. The
controller already answers a validation error with 400. A chat model asked to
rerank therefore reads as a caller mistake. It does not read as a gateway
short of capacity.

`routeOperation` reached the shared mapper when it dropped its own copy of the
collapse. That also gives every operation on the path the modality refusal it
used to lose.

## The document bound rides on the route

Cohere and Voyage both cap the document list, and the cap belongs to the
offering rather than to the model. Two offerings of one model state different
ones, so a single number anywhere above the planner would be wrong.

`routing.Candidate` and `routing.Route` gained `MaxDocuments`.
`toPlanningCandidates` reads it from `offering.Limits`, and the planner copies
it onto the route it chose. The planner reads it nowhere else.

`providerCall` gained an optional `bounded` hook. The rerank call sets it to
`CheckDocumentBound`. The hook runs before the call builds its request and
before it invokes the transport. A list the provider would reject therefore
costs no credential and no round trip. The refusal is a validation fault, which
answers the caller rather than reporting an unavailable gateway. It stays
retryable, so a second offering with a larger bound still gets its turn.

Zero means the catalog states no bound. Reading it as "no documents allowed"
would refuse every request to a model the catalog has not described yet. A
negative bound is a snapshot error, in the same class as a negative context
window.

## Tests

Six in `internal/routing/rerank_test.go`:

- `TestThePlannerRefusesARerankRequestToAChatModel` asserts the typed refusal
  and that the message names the route and the operation.
- `TestARerankModelPlansEveryOfferingThatServesIt` is the other half. A refusal
  that fired for every model would pass the test above and route nothing.
- `TestTheDocumentBoundRidesOnTheChosenRoute` reads two different bounds off two
  offerings of one model.
- `TestAnUnstatedDocumentBoundIsSilenceRatherThanZero` covers the catalog that
  states none.
- `TestASnapshotWithANegativeDocumentBoundIsInvalid` covers the other end.
- `TestTheRerankOperationSpellsTheCatalogName` pins this package's operation
  name to Starmap's. The package keeps its own vocabulary so that a plan
  depends only on the values handed to it. That test makes the copy safe
  rather than hopeful.

Two in `internal/router/rerank_projection_test.go`.
`TestRoutePlanFailureKeepsTheOperationRefusal` covers the classification.
`TestTheDocumentBoundRefusesBeforeTheProviderCall` runs the real attempt
closure and asserts that the transport was never invoked and no credential was
bound.

One in `internal/proxy/operation_failure_test.go` covers all three answers this
seam produces: the operation refusal, the uncatalogued name, and everything
else.

## Evidence

| Check | Result |
| --- | --- |
| `go test ./...` | exit 0 |
| `go vet ./...` | exit 0 |
| `make lint` | 0 issues |
| `scripts/verify-reranking.sh` | 16 passed, 6 failed |
| `scripts/verify-dependency-direction.sh` | exit 0 |
| `scripts/verify-v1-architecture.sh` | exit 0 |
| Every other gate in the required-evidence list | exit 0 |

The two conditions this task owns, `RNK-V13` and `RNK-V14`, both pass. The six
that remain are `RNK-V15` through `RNK-V20`, which RNK8 through RNK10 own.

The full gate list ran this time. RNK6 skipped `scripts/verify-document-parser.sh`
and CI caught what that gate holds.
