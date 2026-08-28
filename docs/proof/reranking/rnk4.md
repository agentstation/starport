# RNK4 Transport and connector

Reranking now reaches a provider. The Starmap pin moved to `v0.13.0`,
`routing.OperationRerank` joins the served set, and two transports compose from
the production registry.

## The problem the task stated was stale

The task text described three hardcoded guards: one in the transport registry
and two in `internal/routing/planner.go`. The model modalities plan replaced all
three before this task started. One named set, `routing.ServedOperations()`,
answers every question the gateway asks about an operation, and the planner
reads it once per candidate.

Decision RNK-D6 anticipated this. It states that whichever of the two plans
lands first introduces the named set, so step one of this task reads *introduce
or extend*. The extension is one constant and one line in the set.

The ordering the guard bought still held. Starmap `v0.13.0` shipped five rerank
offerings before Starport could plan one, and the planner treated them as inert
rather than as corruption. No chat route changed.

## Why the connector gained no method

Step three read *add the rerank call to the connector contract beside the
embedding call*. Decision RNK-D12 replaces it.

Seven transports compile. Two serve reranking. A method on `Connector` would give the other five a call they cannot answer. Each would then need a body that refuses, and the compiler could no longer check them. The package already states
the opposite rule for an optional operation, and
`TestConnectorGainedNoMethodForMedia` pins `Connector` at five methods.

Reranking arrives as `Reranker`, a one-method interface, plus `RerankerFor`,
which reports false for a transport that omits it. The registry proves the
pairing: a descriptor that declares `rerank` and whose transport does not
implement `Reranker` fails to compose, and
`TestADescriptorCannotDeclareRerankWithoutTheInterface` holds that.

## One HTTP path and two codecs

Cohere and Voyage AI disagree on four things. The result count is `top_n` or
`top_k`. The ranked list is `results` or `data`. The billing unit is a search
unit or a token. The per-document cap exists or it does not.

They agree on the rest: one POST, a bearer credential, a JSON body carrying a
model, a query, and a document list. Two whole transports would repeat that
request twice, and one transport branching on the provider would put a provider
name in shared code.

The split is two types. The type `rerankConnector` owns the request, the credential placement, the status check, and the reader that parses a rejection. The type `rerankCodec` owns every wire word for one provider.

The test `TestBothRerankProtocolsSpeakOneRequest` sends the same canonical request through both transports. It asserts that each body carries its own count field and not the other one. It asserts that both answers reduce to the same results.

## Voyage AI refuses a cap it cannot express

Voyage AI switches truncation on or off. It states no per-document token cap.

A dropped field would bill the caller for whole documents after it asked for less. That is the anti-pattern this plan rejected for `return_documents` at RNK5. The method `voyageRerankCodec.encode` returns `ErrRerankOptionUnsupported` instead. The test `TestVoyageRefusesATokenCapItCannotExpress` fails if the request reaches the server at all.

## The transport refuses a result outside the request

The canonical result names a position rather than a copy of the text. A provider index that falls outside the request therefore resolves to the wrong document or to none. The method `Rerank` checks every index against the document count it sent, and it returns an error that names both numbers.

## The rejection reader

Cohere states a rejection under `message` and Voyage AI states it under
`detail`, which its framework can render as a string or as a list. One reader
covers both. It tries the OpenAI-shaped `error` object first, then `message`,
then `detail` decoded as a string, then the raw body.

`TestARerankRejectionNormalizesLikeAChatRejection` runs four rows through
`NormalizeFailure`. A Cohere 401 becomes `failure.Authentication` scoped to the
credential. A Voyage 429 becomes `failure.RateLimit` scoped to the offering and
retryable. A Cohere 400 becomes `failure.Validation` scoped to nothing. A Voyage
502 that returns raw text becomes `failure.ProviderUnavailable`. Each row also
asserts that the provider's own words stay out of `SafeMessage`.

Decision RNK-D13 records why the condition reads this package rather than
`internal/failure`. The vocabulary package imports no provider code, so a rerank
test there could only assert that reranking adds no failure class.

## The catalog projection

`internal/catalog/rerank_projection_test.go` reads the shipped generation
through the same census the media projection test uses.

`TestRerankOfferingsProjectWithTheirPriceAndBound` walks every route that serves
reranking and requires an endpoint URL, a positive `MaxDocuments`, and a price
that matches the offering's stated basis. A search-unit offering needs
`SearchUnit`, and a token offering needs an input token price. The test also
requires both bases to be present, because a generation carrying one of them
would leave the other branch unexercised.

`TestARerankOfferingServesNoChatOperation` requires a rerank route to name
`rerank` and nothing else. Every chat surface filters on the operations a route
names, so that is the fact that keeps a reranker out of the model picker.

## Evidence

| Check | Result |
| --- | --- |
| `go test ./...` | exit 0, 47 packages ok |
| `go vet ./...` | exit 0 |
| `make lint` | exit 0, 0 issues |
| `bash scripts/verify-reranking.sh` | 7 passed, 15 failed |
| `bash scripts/verify-catalog-driven-providers.sh` | 19 passed, 0 failed |
| `bash scripts/verify-dependency-direction.sh` | 6 passed, 0 failed |
| `bash scripts/verify-package-layout.sh` | passed |

Conditions `RNK-V01` through `RNK-V04`, `RNK-V06`, and `RNK-V07` are green.
Phase A now shows its four conditions passing, which decision RNK-D10 deferred
to this task.

## One guard the change moved

`TestTransportAuthenticationRegistriesUsePrimitives` pins the endpoint types the
production registry compiles. It listed five. It lists seven now. The list is
the point of the test, so the update is the intended report rather than a
weakened assertion. Both new types authenticate through the existing bearer primitive, and the test's own primitive list holds the same five entries.

## The verifier repair

The helper `no_local_operation_table` carried a definition and no caller. Its path list included `internal/routing`, which refuses the operation constant this task needs. That package imports no catalog package and restates the names as literals.

Removing the helper would lose the check. Widening `internal/routing` would undo the dependency-direction work of three recent commits. The helper now reads `internal/catalog` and `internal/inference`, where a third spelling would be an unheld source. Condition `RNK-V01` calls it through a composite that also requires the Starmap name. The terminal count stays at 22.
