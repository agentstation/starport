# PLG8 What a page cost

An operator could not see what reading a document cost. The gateway recorded
every fact and no view held them. A page charge that appears in no view reads as
an unexplained rise in spend.

The console now answers the question at `/documents`. It names the engine, the
page count, the time, and the cost of each document read. It also lists the
recognition models this catalog reaches, with the price of one page at each.

## Three facts never reached the console

The panel needed data that three separate defects held back. Each one was
silent, and each one produced a plausible screen rather than an error.

The projection dropped the operations list. The type `openrouter.ModelOffering`
carried no such field, so `openRouterOffering` discarded it on every response.
The console read an empty list for every offering. This broke more than the new
panel. The job view filtered video models against that list, and the model
detail drew its operation chips from it.

The projection also swallowed a page price. The function `offeringPricing`
returned early when an offering published no token price. A recognition-only
offering publishes exactly that, so a priced page reported no price at all.

The usage record lost the cache flag. The response carried `ExtractionCached`
and `applyExtraction` never copied it. A cached read and a native read both
record no cost. Only that flag separates a page an earlier turn paid for from a
page no provider ever charged for.

## A shape test catches a dropped field

The controller already held one test that compared the JSON names of the
pricing block against the projection it mirrors. The offering that holds the
block had no such guard, which is how the operations list went missing.

`TestOpenRouterOfferingsMirrorTheCatalogProjection` widens the guard to the
whole offering. It compares field names, and then encodes one offering and
asserts that every projected name survives the conversion. A dropped field is
invisible in a response, so a reader cannot find this class of defect.

## Every label comes from the counts

The panel reads `document_pages`, `recognized_pages`, and `native_pages`, and it
never consults the engine name. An engine this console has not heard of still
renders a true row.

The cost cell gives four unlike answers. A cache hit says so. A priced read
shows its amount. A read with recognized pages and no price says `unpriced` and
names the reason the gateway gave. A read with no recognized pages says the
pages cost nothing, because the process read them.

A bare zero for three of those cases would tell a reader that a paid page was
free.

## One filter, two pickers

A recognition model answers no chat turn. It reaches this gateway through the
`file-parser` plugin, so a picker that offers one hands the reader a routing
refusal that names nothing they did wrong.

The repository holds two pickers. The chat popover lives at
`console/src/components/chat/ModelPicker.tsx`, and the preset combobox lives at
`console/src/components/models/ModelPicker.tsx`. The filter belongs to neither,
so `chattableModels` sits in `console/src/lib/modelFilter.ts` beside the
capability readers that already serve both.

The rule reads the operations of each offering. A model stays when any offering
serves anything other than recognition. A model with no offerings stays too,
because that is a catalog this console could not read rather than a model that
serves nothing.

The first version of the rule asked whether an offering includes recognition.
That excluded Gemini, which reads documents and answers chat. The test caught
it, and the rule now asks what the list holds apart from recognition.

## Evidence

| Command | Result |
| --- | --- |
| `pnpm --dir console check` | build ok, `tsc --noEmit` clean, 144 tests passed |
| `bash scripts/verify-console-modernization.sh` | 21 passed, 0 failed |
| `bash scripts/verify-document-parser.sh` | 18 passed, 2 failed |
| `go test ./...` | exit 0 |
| `go vet ./...` | clean |
| `make lint` | 0 issues |
| `make build` | `Build complete: ./starport` |

PLG-V18 is this task's own condition and it passes. PLG-V19 and PLG-V20 belong
to PLG9.

## Tests

The file `console/src/components/documents/DocumentsPanel.test.tsx` holds seven
tests:

- A recognized request renders its engine, its pages, and its cost.
- A cache hit renders as a saving rather than as an empty cost.
- A natively read document says the pages cost nothing.
- A page the gateway could not price says so rather than showing nothing.
- A request that attached nothing is not a document read.
- The catalogued recognition models render with their page price.
- A catalog that serves no recognition says so.

The cost assertion checks the extraction share and rejects the turn total. The
column exists to separate what reading the document cost from what answering it
cost, and a cell that showed the total would defeat that.

`console/src/components/chat/ModelPicker.test.tsx` adds the picker case. A
recognition-only model never appears, and a model that reads documents beside
chat still does.

Three Go tests cover the defects above. The file
`internal/catalog/view/page_price_test.go` holds the page price cases.
The file `internal/usage/extraction_test.go` records a cached read as cached
rather than free. The file `internal/proxy/parser_usage_test.go` drives two
turns over one document, and it asserts that the second turn reads the cache and
charges nothing.

That last test needs the catalog context helper. The extraction cache key names
the catalog generation, so a turn with no catalog in force caches nothing and
the second turn pays again.
