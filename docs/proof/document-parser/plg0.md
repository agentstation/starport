# PLG0 Baseline

## Outcome

`scripts/verify-document-parser.sh` reports `Summary: 0 passed, 20 failed`.
`docs/proof/document-parser/` exists. This task changes no production file.

## The pinned baseline

```
starport  42e8b570eb85791217755c0435e176cf24dc274e
starmap   0414a3cd1554d714e5d3b088323f7d22665011c1
go.mod    github.com/agentstation/starmap v0.11.0
```

## The engines OpenRouter states today

Read on 2026-08-27 from
`https://openrouter.ai/docs/features/multimodal/pdfs`, and confirmed against
`https://openrouter.ai/docs/guides/features/plugins/overview`.

| Engine | Price | What it does |
| --- | --- | --- |
| `mistral-ocr` | 2 US dollars for each 1,000 pages | Best for a scanned document or a PDF that carries images |
| `cloudflare-ai` | Free | Turns a PDF into markdown on Cloudflare Workers AI |
| `native` | Charged as input tokens | Only for a model that reads a file directly |

The request shape is one plugin entry:

```json
{ "plugins": [ { "id": "file-parser", "pdf": { "engine": "mistral-ocr" } } ] }
```

A request that names no engine gets the model's own file reading first.
OpenRouter falls back to `cloudflare-ai` when the model has none.

The plan warned that a vendor replaced one engine. It did. The third engine
used to be `pdf-text`. That name is gone from the current
documentation, and `cloudflare-ai` stands in its place.

## Why Starport will not accept those three names

Two of the three name a vendor this gateway does not route to.

Starport has no Mistral offering. `providers/mistral/` in the Starmap catalog
holds a logo and nothing else, so `mistral` serves zero models. The ten Mistral
author models that do have endpoints all resolve to `deepinfra`. Cloudflare is
not a catalogued provider at all. It appears in `provenance.yaml` under two
rejected identifiers, `cloudflare-workers-ai` and `cloudflare-ai-gateway`, and
both carry an empty endpoint.

Accepting `mistral-ocr` and then routing to another vendor is the unkept
promise invariant P2 forbids. Decision PLG-D4 already chose refusal over a
silent fallback. So the enforced vocabulary is two names: `native` for the
in-process read, and `recognition` for a catalogued offering. Every other
engine name draws a typed refusal that says what this gateway runs.

Condition `PLG-V02` holds both halves. It requires the two constants, and it
requires that no accepted value in `internal/` spells `mistral-ocr`.

## The recognition census is empty

No provider this deployment can reach with a credential serves a document
recognition operation. Nothing does, because the operation does not exist.

`ProviderOperation` in `pkg/catalogs/provider.go:149-168` names exactly eight
operations. None of them recognizes a document. Validation reads
`mediaOperationFacts` in `pkg/catalogs/media_operations.go:30-69` rather than a
switch, so one table row adds the operation, its error message, and its
derivation together. That is the PLG3 change.

Three catalogued models carry `ocr` in the name. All three answer
`chat-completions` and price per token.

| Model | Provider | Operation | Credential here |
| --- | --- | --- | --- |
| `qwen/qwen-vl-ocr` | `alibaba` | `chat-completions` | none |
| `qwen/qwen-vl-ocr-2025-11-20` | `alibaba` | `chat-completions` | none |
| `google/pretrained-ocr` | `google-vertex` | `chat-completions` | none |

`mistral-ocr-2505` exists as an author record with no provider offering and no
endpoint. Its declared modalities read `text` in and `text` out, which is wrong
for a recognition model.

The credentials this deployment holds name three providers: `google-ai-studio`,
`groq`, and `anthropic`. Their document-capable fleets are below. The count is
the number of provider model files that declare the modality as input.

| Provider | Models | Image input | PDF input |
| --- | --- | --- | --- |
| `google-ai-studio` | 54 | 45 | 19 |
| `anthropic` | 10 | 10 | 10 |
| `groq` | 10 | 2 | 0 |

So PLG3 has one reachable candidate to carry the first recognition offering:
`google-ai-studio`. Its vision models take an image, and nineteen of them
already take a PDF. The `anthropic` provider is reachable by credential. Its Go client
fails TLS verification from this machine, which rules it out as the proof
provider. The `groq` provider serves no PDF input at all.

## There is no per-page price field

`ModelOperationPricing` in `pkg/catalogs/model_pricing.go:193-211` holds ten
prices. Each one is per request, per media unit, or per tool call. None is per
page.

Decision PLG-D2 exists because of that gap. An offering that priced a page by
the token would bill a caller by output length rather than by document size. The nearest sibling is `ImageInput`, which already prices one unit of
media. A `PageInput` field sits beside it. The type `ModelPricingTier` in the
same file carries its own `Operations` block, so the new price becomes tierable
with no extra work.

Condition `PLG-V07` requires the field and the operation together in
`internal/catalog`. Condition `PLG-V08` requires a projection that refuses an
offering naming the operation with no page price.

## Starmap spells the modality pdf

`ModelModality` in `pkg/catalogs/model.go:277-284` names six modalities:
`text`, `audio`, `image`, `video`, `pdf`, and `embedding`. There is no
`document` modality. `projectModalities` in Starmap's
`internal/server/openrouter/project.go:113-122` renames `pdf` to `file` on the
OpenRouter wire, which matches the wire word the modality plan already chose.

## Fail-before: where the codec reports the plugin as unkept

`internal/protocol/openrouter/codec.go:58` holds the field:

```go
Plugins             []json.RawMessage    `json:"plugins,omitempty"`
```

The comment at `codec.go:265-267` states why nothing forwards it. Transforms
and plugins name OpenRouter gateway work rather than provider request fields.
Sending one upstream makes a provider refuse a request it would otherwise
serve.

`unenforcedGatewayFields` in `provider_prefs.go:128-147` turns that into a
report. The `plugins` arm sits at line 142. The function `unenforcedFields` at
`provider_prefs.go:151-161` merges the gateway list with the provider list and
sorts it. The chat controller returns the result in
`X-Starport-Unenforced-Provider-Fields`.

The test at `provider_prefs_test.go:90` still expects `plugins` in that list.
PLG7 owns the change, and that line is its fail-before evidence.

## Evidence

```
bash scripts/verify-document-parser.sh   Summary: 0 passed, 20 failed
go build ./...                           clean
git grep -c ParserEngine                 0 files
git grep -c documents-recognition        0 files
git grep -c PageInput                    0 files
docs/proof/document-parser/              created
```

This task did not run the wider gate list. It adds one script and one proof
file and touches no production file, so nothing moved in the request path.
Mark the rest UNVERIFIED for this task.
