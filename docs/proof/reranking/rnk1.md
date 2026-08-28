# RNK1 The Starmap rerank operation

Starmap names ten provider operations. The tenth is `rerank`, so a catalog can
now state that a provider ranks documents. The change is in Starmap alone.

## What changed

The file `pkg/catalogs/provider.go` adds `ProviderOperationRerank` with the wire
value `rerank`. The provider contract accepts the member, and the refusal
message lists it beside the other nine. An endpoint that names the operation
still has to state a path, and a test holds that rule.

The file `pkg/catalogs/model_tags.go` adds `ModelTagRerank`. Two files carried
the word as a bare string before this task. Both now read the constant.

The file `pkg/catalogs/offering_views.go` derives the rerank operation from that
tag. The chat completions view still refuses the same model, which is invariant
R3 of the plan.

The file `pkg/catalogs/model_pricing.go` adds `SearchUnit` and `RerankBasis` to
`ModelOperationPricing`. Both travel through the two deep copy paths that every
other operation price travels through.

## Why the tag decides, and not the modalities

Starmap already holds a table of dedicated media operations. Each row states
the input modalities a model must declare and the exact output set it must name.
A reranker reads text and writes text, which is also the shape of a chat model.
The modalities separate nothing here, so the table cannot carry the row.

The tag does the work instead. A model that carries `rerank` serves the rerank
operation and no chat operation. A model with the same modalities and no tag
stays a chat model. One test asserts both halves, because a reranker in the chat
list would receive a request it cannot answer.

## The two billing bases

RNK0 read four providers and found two billing bases. Decision RNK-D4 therefore
asks the catalog to record which unit it means. The field `RerankBasis` records
it, and the price validator holds the pair together.

| Recorded basis | The validator requires |
| --- | --- |
| `search-unit` | a `search_unit` price |
| `token` | an input token price |
| absent | that no `search_unit` price is present |
| any other value | nothing, because it names the value and refuses |

The last two rows matter most. A price with no stated basis would leave a
consumer to guess the unit. A basis no constant names would reach
`internal/usage` as a silent default.

## The endpoint data moved to RNK2

RNK1 planned to add a rerank path to a provider record. No provider in the
shipped catalog serves reranking. The catalog holds fifteen providers, and RNK0
read four that publish a rerank endpoint: Cohere, Jina, Voyage, and OpenRouter.
None of the four is in the catalog today.

The contract work still landed in this task. An endpoint that names `rerank`
validates, it needs a path, and it accepts an author-specific path. The data
lands in RNK2 beside the providers that carry it. Decision RNK-D10 records the
move.

## When the phase A conditions turn green

The verifier `scripts/verify-reranking.sh` reads the Starport tree alone. RNK1
and RNK2 change Starmap, so conditions RNK-V01 through RNK-V04 stay red until
RNK4 raises the dependency and Starport reads the new facts. The plan's phase
table now says so. The gate still reports 0 passed and 22 failed.

## Evidence

| Command | Result |
| --- | --- |
| `go test ./pkg/catalogs/...` | ok, 9 packages |
| `go test ./...` | exit 0, 65 packages ok |
| `go build ./...` | clean |
| `bash scripts/verify.sh` | repository verification passed, exit 0 |
| `bash scripts/verify-reranking.sh` | 0 passed, 22 failed, unchanged |

Every Starmap command ran with `GOTOOLCHAIN=go1.25.12`, which is the toolchain
its continuous integration pins.

The gate failed once. It reported `internal/embedded/openapi/openapi.json is
stale`, because the operation and the tag are both enumerated in the published
schema. The command `make openapi` regenerated the document, which added three
enum entries and no other change. The second run passed.

Autoreview ran at gate `pre-pr` with reviewer `gpt-5.6-sol` and reported no
accepted findings.

## Fail-before

At the baseline commit `7265f4e6` the package names no `ProviderOperationRerank`
and no `ModelTagRerank`. The new file `pkg/catalogs/rerank_test.go` therefore
fails to compile against that tree. A catalog that named the operation failed
validation with a message that listed nine members.
