# RNK2 Rerank offerings and prices

The shipped catalog now holds five rerank offerings from two providers. RNK1
named the operation. RNK2 gives it data, so a route planner has somewhere to
send a rerank request.

## The census

| Fact | Before RNK2 | After RNK2 |
| --- | --- | --- |
| Providers in the shipped catalog | 15 | 17 |
| Providers that publish a rerank endpoint | 0 | 2 |
| Rerank offerings | 0 | 5 |
| Rerank offerings with a price | 0 | 5 |
| Rerank offerings with a document bound | 0 | 5 |

The two providers are Cohere and Voyage AI. Each carries one inference endpoint
that names the rerank operation and states a path.

## The offerings

| Offering | Basis | Price | Documents | Tokens per document |
| --- | --- | --- | --- | --- |
| `cohere/rerank-v3.5` | search unit | $0.001 | 1000 | 4096 |
| `cohere/rerank-v4.0-fast` | search unit | $0.002 | 1000 | 4096 |
| `cohere/rerank-v4.0-pro` | search unit | $0.0025 | 1000 | 4096 |
| `voyage/rerank-2.5` | token | $0.05 per million | 1000 | 32000 context |
| `voyage/rerank-2.5-lite` | token | $0.02 per million | 1000 | 32000 context |

Both billing bases ship. The catalog would satisfy the price rule with one of
them, and a consumer that read whichever price it found would still be right.
The second basis is what makes the `RerankBasis` field load-bearing.

## Why Jina waits

Decision RNK-D8 named Cohere, Jina, and Voyage. Jina publishes a rerank
endpoint and five model IDs, and it publishes no first-party price for any of
them. The only quoted figure comes from a reseller, and a reseller price is a
fact about the reseller. Decision RNK-D11 defers the Jina offerings until Jina
publishes a per-model rate.

OpenRouter also serves reranking. It is a provider Starport reaches through the
OpenRouter protocol rather than a catalog inference endpoint, so RNK5 owns it.

## Two endpoint types, not one

Reranking has no OpenAI standard. OpenAI publishes no rerank endpoint, so
`EndpointTypeOpenAI` cannot mean "the shape everyone copied". Cohere's request
is that shape, and OpenRouter copied it, so this task adds
`EndpointTypeCohere`.

Voyage differs in both directions. It names the result count `top_k` where
Cohere names it `top_n`, and it returns the ranked list under `data` where
Cohere returns `results`. A single type would tell a connector nothing, so
Voyage gets `EndpointTypeVoyage`.

Neither type has a compiled catalog-acquisition transport. The provider
contract refuses both for acquisition and accepts both for inference, which is
the same treatment `EndpointTypeOllama` already receives.

## Two new limits

`ModelLimits` gains `MaxDocuments` and `DocumentTokens`. A reranker refuses a document list longer than its bound rather than truncating it. A caller that cannot read the bound sends a request that cannot succeed. The token bound is a cost fact as well as a size fact. A provider that exceeds it splits the document into chunks it bills separately.

Adding a limit touches six places in Starmap. One of them is
`pkg/differ/differ.go`, which walked a hardcoded list of three limits and had
already fallen behind by one: it never reported a change to `DocumentPages`.
The loop now walks `catalogs.PublishedModelLimits()`, a new exported accessor
over the order the package already maintains. The next limit therefore reaches every consumer that reads a diff.

## Evidence

| Command | Result |
| --- | --- |
| `go test ./internal/bootstrap/ -run Rerank` | 2 tests, both pass |
| `go test ./...` | exit 0 |
| `go build ./...` | clean |
| `starmap validate catalog` | providers, authors, models, cross-references all pass |
| `bash scripts/verify.sh` | repository verification passed, exit 0 |
| `bash scripts/verify-starmap-ownership.sh` | pass, in Starport |
| `bash scripts/verify-reranking.sh` | 0 passed, 22 failed, unchanged |

Every Starmap command ran with `GOTOOLCHAIN=go1.25.12`, which is the toolchain
its continuous integration pins.

The reranking gate reads the Starport tree alone, so it stays red through phase
A. RNK4 raises the dependency, and conditions RNK-V01 through RNK-V04 turn
green there.

## What the gate caught

Three guards failed before this task passed, and each one names a rule the new
data had to satisfy.

The identity map at `docs/reviews/P5_PROVIDER_MODEL_IDENTITY_MAP_2026-07-28.yaml`
holds one reviewed record per provider model file. Five new files needed five
new records, and the count moved from 613 to 618.

Every model file has to state the complete Boolean capability surface of
`ModelFeatures`. A reranker supports none of the generation controls, and the
guard wants the answer written down rather than inferred from absence.

The pinned artifact consumer in `testdata/consumers/pinned-artifact` holds the
exact archive digest of the embedded generation. Any catalog change moves it.

## Fail-before

At the baseline commit `befb6e75` the shipped catalog holds no rerank offering.
The census in `rnk0.md` reports zero. Both new tests in
`internal/bootstrap/rerank_offerings_test.go` fail against that tree: the first
on "the shipped catalog holds no rerank offering", the second on "no shipped
provider publishes a rerank endpoint".
