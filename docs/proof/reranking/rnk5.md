# RNK5, the rerank codecs

Task RNK5 adds the two codecs that own every rerank wire name. The
Cohere-shaped one serves `POST /v1/rerank`. The OpenRouter-shaped one serves
`POST /api/v1/rerank`. No route reaches either codec yet. Task RNK6 registers
both, so this task ends with two codecs and their tests alone.

## Why two codecs and not one

The two wire shapes disagree on three things, and each disagreement changes
what the encoder has to hold.

A document is a string on the `/v1` route. On the OpenRouter route it is a
string or an object that names its own kind. The OpenRouter decoder reads both
and refuses an image, because a reranker scores text and the transport has no
field for a picture.

An echoed document is optional on the `/v1` route and required on the
OpenRouter route. The `/v1` encoder therefore takes a decoding that carries the
caller's `return_documents` flag. The OpenRouter encoder takes the request
directly, because it echoes on every result and has no flag to read.

A result count is `top_n` on both routes, but the OpenRouter route also accepts
the provider preference block every other OpenRouter route accepts. One
validator covers all of them, so the rerank decoder calls the same
`validateProviderPreferences` and `unenforcedProviderFields` the chat decoder
calls.

## The echoed text comes from the request

The canonical `RerankResponse` holds an index and a score. It holds no text.
Invariant R5 states why. A second copy of the document would travel through
routing, the cache, and the usage record. The two copies could then disagree.

Both encoders therefore call `RerankResponse.Documents(request)`. It resolves
each index against the request the gateway already holds. The test
`TestAnEchoedDocumentComesFromTheRequest` asserts both directions. A caller that
asked reads its own text back. A caller that did not ask reads a result with no
`document` member at all.

## Two answers the codec refuses to publish

`RerankResponse.Validate(request)` is new on the canonical type, and both
encoders call it before they write.

An index outside the request resolves to the wrong document. A caller that
ranked on it would rank the wrong text and read an ordinary answer.

A score outside zero through one sorts against every other provider's scale.
Every rerank provider publishes a normalized score, so a number outside the
interval is a decoding fault rather than an unusual answer.

Both faults produce output that reads as correct, which is why the codec refuses
rather than repairs. `ErrRerankScoreOutOfRange` is new. `ErrRerankResultOutOfRange`
arrived at RNK3.

## The document bound lands here and applies later

RNK5 step 2 asks the codec to refuse a document list longer than the offering
allows. RNK7 step 5 carries the document limit onto the route candidate. A codec
cannot read a candidate, so the two steps disagreed on order.

Decision RNK-D14 records the split. The method
`RerankRequest.CheckDocumentBound(limit)` lands here, with its own test. RNK7
wires the caller that reads the limit off the candidate and applies it. A limit of zero or less states no bound, which is what
a catalog says when the provider publishes no document count.

## The search unit reaches the canonical usage record

The OpenRouter rerank schema reports `search_units`. Cohere bills in them, and
no token total converts into one, so the answer cannot compute the field from
what `inference.Usage` already held.

Decision RNK-D15 records why `inference.Usage` gains a `SearchUnits` field.
RNK5 reports the count. RNK8 prices it, applies the spend bound, and adds `cost` to the OpenRouter
usage block. Until then the wire shape names no cost. The codec reports the two
counts the provider sent and omits the one it did not.

## The codec reports a misspelled field

Both decoders call `decodeStrict`, which the chat routes already use. It
refuses an unknown field and a body that holds more than one JSON value, so
RNK5 step 4 needs no new code.

The refusal matters more on this route than on most. Both optional rerank
fields are cost controls. A caller that writes `topn` and decodes in silence
pays for every document rather than the count it wanted to buy.

## Two literals the change made shared

The rerank answers pushed two string literals to a third occurrence, which the
`goconst` linter reports. Both fixes name what the literal means rather than
suppress the report.

The `/v1` package held `StoredFileListObject`, whose value is `list`. Four
shapes carry it now: a stored file page, an embedding set, a video job page,
and a rerank answer. The constant is therefore `ListObject`, and all four sites
read it. No package outside `internal/protocol/openai` named the old spelling.

The OpenRouter package spelled `text` at three sites: a content part, a
response format, and the new rerank document. The constant `contentTypeText`
now sits beside `contentTypeImageURL`, which the package already held.

## Evidence

| Check | Result |
| --- | --- |
| `go test ./internal/protocol/...` | ok, openai and openrouter |
| `go test ./...` | pass |
| `go vet ./...` | clean |
| `make lint` | clean |
| `bash scripts/verify-v1-architecture.sh` | 12 passed, 0 failed |
| `bash scripts/verify-reranking.sh` | 10 passed, 12 failed |

The rerank gate moved from 7 passing to 10. This task owns conditions RNK-V08,
RNK-V09, and RNK-V21. RNK6 through RNK10 own the 12 that remain.

## What the wire assertions hold

RNK-V08 and RNK-V21 both grep for a wire literal in a test file. A test that
asserted only Go field names would pass a rename that broke every caller. Each
round-trip test therefore marshals the encoded answer and compares the JSON. The
`/v1` test pins `relevance_score`, `index`, `object`, and both usage counts. The
OpenRouter test pins the same names plus the required `document` member and
`search_units`.
