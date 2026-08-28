# RNK0 Baseline

The verifier `scripts/verify-reranking.sh` reports 0 passed, 22 failed. No
production file changed. The reading below dates from 2026-08-27.

## Baselines

| Repository | Commit |
| --- | --- |
| starport | `cf17df8` plan: close the document parser plan (PLG10) (#259) |
| starmap | `7265f4e6` feat(catalogs): name the document recognition operation |

## Fail-before census

The word `rerank` appears in four Starport files. All four are plan or proof
documents. No Go file, no route, and no console file names it.

`ProviderOperation` in Starmap names nine members. The list runs from
`chat-completions` to `documents-recognition`, and it holds no rerank member.
The shipped catalog holds zero rerank offerings. The word `rerank` appears once
in `pkg/catalogs/offering_views.go:166`, where it disqualifies a model from the
chat completions view.

`ModelOperationPricing` names ten price fields. None of them is a search unit.

A request to `POST /v1/rerank` reaches no route.

## The provider request shapes

Four providers publish a rerank endpoint. The table records what each one
accepts.

| Provider | Path | Result count | Echo field | Token cap field |
| --- | --- | --- | --- | --- |
| Cohere v2 | `POST https://api.cohere.com/v2/rerank` | `top_n` | none | `max_tokens_per_doc`, default 4096 |
| Jina | `POST https://api.jina.ai/v1/rerank` | `top_n` | `return_documents` | none |
| Voyage | `POST /v1/rerank` | `top_k` | `return_documents`, default false | `truncation`, default true |
| OpenRouter | `POST https://openrouter.ai/api/v1/rerank` | `top_n` | none, always echoes | none |

Every provider requires `model`, `query`, and `documents`. Cohere and Voyage
both cap the document list at 1000. Voyage caps the query at 8000 tokens on its
newest model. OpenRouter requires at least one document. It also accepts a
`provider` block of routing preferences.

Three providers accept a plain string for each document. OpenRouter also
accepts an object that carries `text`, `image`, or both, which serves a
multimodal reranker.

Each response names a document by its index in the request and gives it a
relevance score. Cohere normalizes each score into the range 0 through 1 and
says so. OpenRouter marks the echoed `document` object as required, so its
response always repeats the text the caller sent.

## The billing bases

| Provider | Basis | Unit definition |
| --- | --- | --- |
| Cohere | search unit | one query against up to 100 documents of 500 tokens each |
| OpenRouter | search unit | reports `search_units` and `total_tokens` and a credit cost |
| Jina | token | one token pool shared across the reader, embedding, and reranker APIs |
| Voyage | token | the response reports `total_tokens` alone |

Cohere splits a document longer than 500 tokens into chunks. Each chunk counts
as one document toward the 100-document unit. A caller that sends 200 short
documents therefore pays two search units for one query.

Published prices at the time of this reading appear below.

| Model | Price |
| --- | --- |
| Cohere Rerank v3.5 | $0.001 per search |
| Cohere Rerank 4 Fast | $0.002 per search |
| Cohere Rerank 4 Pro | $0.0025 per search |
| OpenRouter `cohere/rerank-v3.5` | $0.001 per search |

## Reachable providers

No rerank provider is reachable with a credential this deployment holds. The
environment names keys for Anthropic, Groq, OpenAI, and Gemini, and it enables
a local Ollama. None of those four services publishes a rerank endpoint, and
Ollama still has none.

That reading does not block the plan. The shipped catalog already describes
providers this deployment cannot reach. DeepSeek, Mistral, Moonshot, Cerebras,
and Fireworks are five of them. Reachability is not the standard the catalog
holds elsewhere. Decision RNK-D5 asked for a proof no other operation has to
give, and decision RNK-D8 replaces it below.

## What the reading changed in the plan

Four findings contradict the plan as written. Each one landed as a decision
before any code.

The first finding reopens decision RNK-D3. OpenRouter publishes
`POST /api/v1/rerank`. Its OpenAPI document describes the route. The live
endpoint answers 401 rather than the 404 a missing route gives. The plan
recorded the route as absent and made it a non-goal, and the decision named
this exact reopening condition.

The second finding reopens invariant R5. The OpenRouter schema marks the
`document` object required on every result, so a parity route cannot refuse to
echo. R5 now separates the canonical shape from the wire shape. The canonical
response carries indices and scores alone. A codec that must echo reads the
text back out of the request it already holds.

The third finding qualifies decision RNK-D4. Two of the four providers bill per
token rather than per search unit. Starmap therefore records the basis beside
the price, and `internal/usage` reads the basis rather than assuming one.

The fourth finding replaces decision RNK-D5. The plan proves the request path
against recorded provider transcripts. That is how every other unreachable
provider in this catalog holds its contract.

## Evidence

| Command | Result |
| --- | --- |
| `bash scripts/verify-reranking.sh` | 0 passed, 22 failed |
| `go build ./...` | clean |
| `git grep -l -i rerank` | four documents, no source file |
| `curl -s -o /dev/null -w '%{http_code}' -X POST https://openrouter.ai/api/v1/rerank` | 401 |

## Sources

- [Cohere Rerank API reference](https://docs.cohere.com/reference/rerank)
- [Cohere pricing](https://cohere.com/pricing)
- [Jina Reranker](https://jina.ai/reranker/)
- [Voyage reranker API](https://docs.voyageai.com/reference/reranker-api)
- [OpenRouter rerank reference](https://openrouter.ai/docs/api/api-reference/rerank/create-rerank)
- [OpenRouter OpenAPI document](https://openrouter.ai/openapi.json)
- [Ollama rerank endpoint issue](https://github.com/ollama/ollama/issues/10467)
