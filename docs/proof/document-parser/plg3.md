# PLG3 The recognition operation and its per-page price

## Outcome

`scripts/verify-document-parser.sh` moves from 5 of 20 to 8 of 20. `PLG-V06`,
`PLG-V07`, and `PLG-V08` pass. Starmap now names an operation for optical
character recognition, states what one page costs, and states how many pages a
provider reads in one call. Starport reads all three from one snapshot and
refuses to plan a recognition route it cannot bill.

## What Starmap gained

The operation is `documents-recognition`. It joins `ProviderOperation` beside
the media operations MOD shipped. It carries the same shape those carry: a
required tag, an input modality, and an output modality. The tag is `ocr`, the
input is a PDF, and the output is text.

Two facts landed beside it because a page is not a token:

- `ModelOperationPricing.PageInput` holds the cost of one page a provider reads.
- `ModelLimits.DocumentPages` holds the largest document a provider accepts.

Both facts travel the whole path. The merger copies them, the deep copy copies
them, the price validator checks them, and the presence codec publishes them.
The limit needed one more change than the price did. Four codec sites and the
merge each carried their own literal list of the limits. A fifth limit added to
the struct survives one format and vanishes in another. Those five lists became
one `modelLimitOrder`.

## Why Google needed a derived price

Mistral OCR is the obvious per-page example, and it does not exist in this
tree. Mistral ships no embedded models at all, because its models arrive only
through live acquisition. Hand-authoring one puts a model in the tree that no
refresh produces.

Google Gemini reads a PDF, and Google publishes no price per page. It bills a
page as a fixed 258 input tokens, and it refuses a document over 1000 pages.
Both numbers come from [the Gemini document processing
guide](https://ai.google.dev/gemini-api/docs/document-processing). So the
catalog derives the page price:

```
page_input = 258 * tokens.input.per_1m / 1_000_000
```

A derived number carries one obvious failure. A refresh lands a new input token
price from the provider, and the page price beside it keeps the old number. No
test fails, and every recognition request bills the wrong amount from then on.

`TestEveryRecognitionOfferingCanBeBilledByThePage` in `internal/bootstrap`
recomputes the derivation for every Google offering that names the operation.
The derived value stops being a constant somebody typed and becomes a checked
rule.

## The catalog does not offer what it cannot bill

Fourteen Google models declare PDF input and text output. Three of them carry
no input token price at all, so no page price follows from anything. Those
three stay untagged rather than tagged and unpriced.

The census is 11, and two tests hold it. The bootstrap test fails when the
count moves in Starmap. `TestTheShippedCatalogPricesEveryRecognitionRoute` in
`internal/catalog` fails when the count moves in Starport. A stale module pin
is the case that produces it.

## Recognition broke a shape rather than an invariant

Every media operation MOD shipped reaches a path of its own. One test held that
rule: no offering publishes both a media operation and chat completions. The 11
Google offerings publish both. Gemini reads a scanned page through
`:generateContent`, which is the path a chat turn already uses.

A weaker assertion for every operation gives up a real guard.
`servedThroughChat` names the one exemption instead, and the
`MediaOperationFacts` comment now states the real test. A dedicated media
operation is one a consumer asks for by name and pays for in the operation's
own unit. Reaching a separate URL was never the rule, only the pattern.

## What Starport refuses

One place matches an offering's operations against the compiled adapter, so the
price rule went into `compatibleOfferingService`. The helper
`billableOperation` reads the page price for recognition alone. Every other
operation this build plans charges tokens or requests. The catalog already
requires a token price of any published offering.

An offering with no page price loses the operation and keeps the rest. Taking
the whole model away refuses a caller who wanted chat.

The drop needed a verdict of its own. Sometimes recognition is the only
operation the offering and the adapter share. The old verdict there read
`operation_unsupported`, which blames the build for a gap in the catalog. Those
two have different owners and different fixes, so
`RouteExclusionOperationUnpriced` names the second one.

A stated price of zero is not a missing price. A provider may read a page for
free, and `TestAFreeRecognitionOfferingIsBillable` holds that difference.

## What PLG3 did not do

`OperationDocumentsRecognition` exists in `internal/routing` and stays out of
`servedOperations`. PLG4 owns the route, the connector call, and the two-step
turn. That turn sends a scanned document to a recognition model and its text to
the chat model. Adding the operation to the served set now would advertise a
route no connector answers.

## Checks

Starmap, at `GOTOOLCHAIN=go1.25.12`:

- `go test ./...` passed with no failures.
- `make verify` passed, covering the package layout, catalog ownership,
  dependency direction, and consumer gates.

Starport:

- `go test ./internal/catalog/` passed, including the seven new tests.
- `bash scripts/verify-document-parser.sh` reported 8 passed, 12 failed.
- `bash scripts/verify-starmap-ownership.sh` passed.
- `go build ./...` passed.
