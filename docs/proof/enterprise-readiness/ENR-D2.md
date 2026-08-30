# ENR-D2 proof: the /v1/moderations surface

Date: 2026-08-30. Branch: `codex/enr-d2`.

## What shipped

- Starmap first. PR agentstation/starmap#110 names the `moderations`
  provider operation and the `moderation` model tag. It adds the
  offering validation and the `/v1/moderations` endpoint on the
  OpenAI service. Four omni-moderation model YAMLs carry the tag.
  The offerings publish no pricing, because OpenAI serves the
  operation free.
- `internal/inference/moderations.go` owns the canonical shape. The
  constructor refuses an empty input list. `Validate` refuses a
  result count that disagrees with the input count. It also refuses
  a category score outside the unit interval.
- `internal/routing` gains `OperationModerations` in the served set.
  A chat model reached by a moderation request draws the typed
  `ErrOperationUnsupported` refusal before any credential is spent.
- `internal/providers/connectors` carries the `Moderator` optional
  interface beside `Reranker`. The OpenAI transport implements it
  and joins the wire's two per-category maps into one sorted verdict
  list. It refuses a result count that disagrees with the inputs.
  The transport registry declares the operation on the OpenAI
  descriptor alone.
- `internal/router` and `internal/proxy` ride the generic operation
  seam: `RouteModerations` through `routeOperation`, and
  `ProcessModerations` through `processOperation` behind
  `ValidateModerationRequest`, which names an empty input by its
  position. The cache layer passes the operation through uncached,
  because a verdict reads inputs by position in one caller's own
  list.
- `internal/protocol/openai/moderations.go` owns the wire. The
  decoder accepts a bare string and a string list. It refuses typed
  input parts by name. A silently dropped image would return a
  verdict on less than the caller sent. The encoder validates before
  it writes.
- `internal/server` mounts `POST /v1/moderations` behind the
  `moderations:write` scope. The scope stands alone for the reason
  the rerank scope does. The anonymous scope set and the console's
  default non-admin key grant it. OpenRouter publishes no moderation
  route, so `/api/v1` gains none and the parity gate stays at 17.

## Metering

The OpenAI moderation response carries no usage block, and the
offering publishes no pricing. The meter records the turn under
`OperationModerations` with a nil cost and the `no_usage` reason.
That is the honest record for a free operation. Activity shows the
turn happened, and no zero-dollar bill claims the provider reported
usage it never sent.
`TestAModerationTurnRecordsItsOperationWithoutInventingACost`
holds the shape against the shipped catalog projection.

## Acceptance evidence

- `bash scripts/verify-enterprise-readiness.sh`: `Summary: 15 passed,
  18 failed`. ENR-V15 turned green, the exact D2 condition.
- `go test ./internal/inference/ ./internal/routing/
  ./internal/protocol/...`: PASS. New tests cover the constructor
  refusals, clone independence, and response validation. They also
  cover the polymorphic input decode, the typed-part refusal, and
  the codec round trip. The planner refusal for a chat model has its
  own test.
- `go test ./internal/providers/connectors/`: PASS. The transport
  tests drive the shipped registry descriptor through an httptest
  provider. They cover the wire body, credential placement, the
  category join, and the result count refusal. They also cover the
  `ModeratorFor` probe on a transport that does not serve the
  operation.
- `go test ./internal/server/`: PASS. The route tests hold the path
  registration, the standalone scope, and the anonymous deployment.
  They hold the 404 for an uncatalogued model. They hold the 503 for
  a catalogued model the test server cannot reach.
- Console: `pnpm test` 210 passed across 33 files after the default
  scope change.

## Commands

- `go test ./...`: PASS.
- `go vet ./...`: PASS. `make lint`: 0 issues. `make build`: PASS.
- `bash scripts/verify-starmap-ownership.sh`: PASS.
- The full `verify-*.sh` battery from the required evidence list:
  all structural gates PASS. `benchmark-overhead.sh` and
  `smoke-first-run.sh`: PASS.

## Scope notes

- Rerank invariants held: `verify-reranking.sh` stays terminal at 22
  and `verify-openrouter-parity.sh` at 17.
- The operation adds no affordability pre-flight. The one compiled
  provider is free, and a token offering states no floor to refuse
  against.
- ENR-D3 rides the same jobs seam the video routes use. Moderations
  stays synchronous.
