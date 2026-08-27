# PLG6 The page meter and the spend bound

A recognized page costs money. The usage record now reports what it cost, and
the spend budget refuses a document it cannot pay for before the provider sees
it.

## What changed

The file `internal/usage/model.go` adds six fields to `Record`. They name the engine and the pages the turn attached. They name the pages a model recognized and the pages this process read. They name the milliseconds the reads took and the recognized share of the cost. The fields sit flat beside the timing fields the record already
carries.

The file `internal/catalog/snapshot.go` adds two price accessors. `PagePriceFor`
answers what one model charges for one page. `LowestPagePrice` answers what the
cheapest offering in the generation charges. Both read `PageInput` from the
offering PLG3 already refuses to project without.

The file `internal/limits/spend.go` holds `Allowance`. It carries what one
holder may still spend, and it refuses one known price against that number with
`ErrSpendLimitExceeded`.

The file `internal/server/budget.go` puts the allowance on the request context.
The gate already computed the number for the spend dimension. A step deeper in the request then refuses expensive work against the same number the response headers report.

The file `internal/proxy/parser.go` counts the pages, prices them, and refuses
the ones the account cannot afford. The file `internal/proxy/usage_capture.go`
folds the result onto the record.

## Two prices, and why they differ

The gateway asks the catalog for a page price twice, and the two answers are
not the same number.

Before the recognition call, it asks for the lowest price any offering
publishes. It has to, because the planner chooses the offering afterward. A bound built on a higher price would refuse work the account could pay for. It would cost a caller a request over a price no provider ever charged. The low
estimate overshoots the bound by at most one document, and the account's own cap
refuses the next request.

After the call, it asks for the price of the offering the planner chose. That
number is what the provider bills, so that number is what the record carries.

## Cost, and the field a budget reads

A spend budget meters one field. `ExtractionCost` reports the recognized share on its own. An operator can then tell what reading the document cost from what answering about it cost. The record adds the same amount into `Cost`, because the budget reads `Cost` alone. A document charge outside it would reach a reader and miss the cap.

Three cases record no extraction cost. A native page costs nothing, because no
provider saw it. A cached page costs nothing, because an earlier turn paid for
it. An unpriced page withdraws the whole record cost under `CostReasonNoPricing`. The projection drops an unpriced recognition offering, so this state means the gateway lost its catalog rather than that the page cost nothing.

## A holder with no budget

`Allowance` carries a `Bounded` flag beside the amount. Most deployments set no
spend budget, and such a holder reads as zero remaining. A zero read as an
exhausted budget would refuse every document turn in an unmetered gateway.

A request that reached the parser without passing the gate reads as unbounded
for the same reason. The gate found no budget, so no budget refuses anything.

## Testing a price the shipped catalog does not publish

No offering in the embedded catalog serves `documents-recognition` yet. A probe over the shipped generation found zero recognition routes and no page price. A proxy test that read the real snapshot would assert nothing.

The parser now reads a small consumer-defined interface, `pagePrices`, with the
two questions it asks. `RoutableSnapshot` satisfies it. Every deployment still
reads the catalog the request carries. A test supplies a fixture instead. The arithmetic and the refusal hold before the first offering ships.

## Evidence

| Command | Result |
| --- | --- |
| `go test ./...` | exit 0 |
| `go vet ./...` | clean |
| `make lint` | 0 issues |
| `make build` | `Build complete: ./starport` |
| `go test ./internal/usage/... ./internal/limits/...` | ok, ok |
| `bash scripts/benchmark-overhead.sh` | p50=0ms p99=0ms over 200 requests |
| `bash scripts/verify-document-parser.sh` | 15 passed, 5 failed |
| `bash scripts/verify-v1-architecture.sh` | 12 passed, 0 failed |
| `bash scripts/verify-dependency-direction.sh` | 6 passed, 0 failed |
| `bash scripts/verify-package-layout.sh` | passed |
| `bash scripts/verify-catalog-performance.sh` | 20 passed, 0 failed |
| `bash scripts/verify-openrouter-parity.sh` | 16 passed, 0 failed |
| `bash scripts/verify-files-api.sh` | 22 passed, 0 failed |
| `bash scripts/verify-model-modalities.sh` | 26 passed, 0 failed |

PLG-V14 and PLG-V15 are this task's own conditions, and both pass. PLG-V16
through PLG-V20 belong to PLG7 through PLG9.

## Tests

The file `internal/limits/spend_test.go` holds five tests over the allowance.
They cover the exact boundary, the refusal past it, the holder with no budget,
the carrier, and the request that passed no gate.

The file `internal/usage/extraction_test.go` holds five tests over the record.
A recognized document counts against the spend total. It reports its pages and its own cost. A natively read one reports pages and no cost. The duration survives the round trip. A turn with no attachment reports no extraction.

The file `internal/catalog/page_price_test.go` holds five tests over the two
accessors. They cover the exact offering price, the operation bound, the
cheapest offering, the generation that prices no page, and the absent snapshot.

The file `internal/proxy/parser_usage_test.go` holds seven tests end to end, and
each counts recognition calls. A call is the unit of cost here. Four of them
carry the acceptance:

- A recognized document records one page and a cost of one page.
- A natively read document records its pages and no cost.
- An account at its bound gets a refusal, and the recognizer never runs.
- The record reports the milliseconds the read took.

The rest state the neighbors. An account that can afford the pages reads them.
An account with no budget keeps every document turn. A turn the gateway cannot price
names the gap rather than reading as free.
