# RNK6 proof: the route, the controller, and the scope

Date: 2026-08-27. Branch: `codex/reranking-rnk6`.

## What landed

Two routes now reach the rerank operation. `POST /v1/rerank` sits in the
OpenAI-compatible block and `POST /api/v1/rerank` sits in the
OpenRouter-compatible block. Both stand behind `rerank:write` alone. The scope
does not ride on `chat:write`. A rerank request reads the caller's own
documents, and a key that only writes chat has no claim on them.

`RerankController` in `internal/server/controllers/rerank.go` serves both. One
type serves two protocols, because the two paths plan one route and reach one
provider. They differ only at the edge. Each protocol decodes its own wire
names and writes its own answer shape, so the handler reads the decoding its
protocol produced. The OpenRouter path also reports the documented provider
fields it accepts and cannot enforce, through the same
`X-Starport-Unenforced-Provider-Fields` header the chat route uses.

`DefaultAnonymousScopes` gained `rerank:write`. A deployment with
authentication disabled reaches the route the same way it reaches chat.

## The operation carriers lost their media names

`internal/router` and `internal/proxy` both held a shared path named for media.
Reranking is not media, and a media name on the type that carries a rerank
answer states something false. The carriers are now `OperationRequest` and
`OperationResponse`. The shared proxy helper is `processOperation`, and the
usage capture reads `operationUsage`. Aliases keep `MediaRequest` and
`MediaResponse`, so the media controller and every media test read the same
names they read before. The test stubs followed: `unsupportedOperations` in the
controller tests and `unroutedOperations` in the proxy tests.

This is decision RNK-D16.

## An uncatalogued model answers 404

A caller who misspells a model used to receive 503 with "no models available",
which tells someone with a typo to wait. The refusal now happens before the
planner, on the shared operation path, and it answers 404.

The lookup behind it is new. `RoutableSnapshot.ResolveRoute` answers "can the
gateway reach this model right now". That is a different question. A
catalogued model whose provider has no credential today is absent from the
routable set. Answering that way would tell an operator their catalog lacks a
model it holds. `RoutableSnapshot.Names` reads the whole generation
instead. It reads the routability verdicts, which cover every offering, and the
catalog definitions, which cover a canonical name.

`internal/catalog` owns the sentinel, because it owns the judgment. The first
draft put `ErrModelNotCatalogued` in `internal/router`. That made
`internal/server/controllers` import the router to map it. Condition V08 of
`scripts/verify-v1-architecture.sh` refuses that import and caught it.

The change reaches every operation on the shared path. Images, speech,
transcription, video, and document recognition now answer 404 for a model no
catalog generation holds. This is decision RNK-D17.

## The parity gate grew

`scripts/verify-openrouter-parity.sh` gained `ORP-V17`. It asserts that the
OpenRouter family registers the rerank route. Its count moves from 16 to 17. `AGENTS.md`
states the new count in both places that name it. The media gate
`scripts/verify-model-modalities.sh` keeps its own count. The split the two
gates hold stays where it was.

## Tests

Five new tests in `internal/server/rerank_routes_test.go`:

- `TestServerRegistersTheRerankPaths` walks the built router rather than
  reading the file that builds it.
- `TestRerankRoutesCarryTheRerankScope` asserts 403 for a key holding chat and
  embeddings, and 400 from the codec for a key holding `rerank:write`.
- `TestAnonymousDeploymentReachesTheRerankRoutes` covers the operator running
  without authentication.
- `TestRerankRefusesAnUncataloguedModel` asserts 404, and that the answer names
  the model.
- `TestRerankAcceptsACataloguedModelName` is the other half. A guard that
  refused every name would pass the test above and break every real request.
  This one sends `cohere/rerank-v3.5` and asserts that the gateway does not
  call it unknown.

Two new tests in `internal/catalog/names_test.go` pin the property that makes
`Names` worth having beside `ResolveRoute`. One asserts that an offering the
planner excluded answers true through `Names` and false through `ResolveRoute`.
The other asserts that a fabricated name answers false.

The server test helper gained `withRoutableCatalog`. Without a catalog
generation the registry carries no snapshot. A route test then cannot separate
a name no catalog holds from a gateway that has not read a catalog yet. The
option is opt-in, so every existing server test composes as it did before.

## Evidence

| Check | Result |
| --- | --- |
| `go test ./...` | exit 0 |
| `go vet ./...` | exit 0 |
| `make lint` | 0 issues |
| `scripts/verify-reranking.sh` | 14 passed, 8 failed |
| `scripts/verify-openrouter-parity.sh` | 17 passed, 0 failed |
| `scripts/verify-auth-onboarding.sh` | 26 passed, 0 failed |
| `scripts/verify-v1-architecture.sh` | 12 passed, 0 failed |
| `scripts/verify-dependency-direction.sh` | exit 0 |
| `scripts/verify-package-layout.sh` | exit 0 |
| `scripts/verify-model-modalities.sh` | exit 0 |
| `scripts/verify-files-api.sh` | exit 0 |
| `scripts/verify-console-session-grants.sh` | exit 0 |
| `scripts/verify-doc-links.sh` | exit 0 |

The four conditions this task owns, `RNK-V10`, `RNK-V11`, `RNK-V12`, and
`RNK-V22`, all pass. They failed before the task, which is the fail-before the
plan asks for.
