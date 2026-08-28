# RNK9 proof: a rerank model an operator can see

Task RNK9 of the reranking plan. Condition RNK-V18.

## Problem

RNK2 put five rerank offerings in the catalog. The console models table showed
each one as a row with a name, four empty price columns, and nothing that said
what it does. An operator confirming the catalog could not tell a reranker from
a chat model that nobody priced.

## What landed

### The operation

`ModelsTable` gained an operations column. It reads `operationsOf`, which
already gathered the sorted union across a model's offerings, and renders one
badge per operation.

`operationLabel` changes the separator and nothing else. Two other designs
failed:

- Shortening to the last word renders `images-generations` and
  `videos-generations` as one label. Two operations collapse into one badge.
- A table of the known names drops whatever the catalog learns next. A row
  with no badge then reads as a model that serves nothing.

The models route gained an operation facet beside the provider, author, and tag
ones. Its options come from the loaded catalog with counts, so an operation this
console has never seen is selectable the day the catalog gains it.

### The price and the limit

A Cohere rerank offering publishes no token price at all. `offeringPricing` in
`internal/catalog/view/models.go` decided from the token block, so it returned
`nil` and the row showed four dashes. Two changes fixed it:

- `OfferingPricingInfo` gained `SearchUnit`. The nil guard now asks whether any
  of the three prices exists, not whether the token one does.
- `ModelOfferingInfo` gained `MaxDocuments`, beside the context window rather
  than inside the price block. It is a shape rather than a cost.

`formatSearchUnitPrice` reports only on an offering whose basis is
`search-unit`. This is the RNK8 meter rule carried out to the reader. A
token-billed offering with a stale figure beside it would price a turn in a unit
the provider never charges. The reader would then take the smaller of two
numbers.

Both facts are per-offering, so they render in the model detail offering table
rather than in the models table. The columns are `Unit price` and `Max docs`.
The helper `unitPrice` names the unit beside the number, because the figure
alone reads as a fifth token price.

The OpenRouter offering shape gained the same two members, and
`openRouterOffering` copies them.

### The chat picker

`chattableModels` excluded a model whose offerings all serve document
recognition. That set is now `SILENT_OPERATIONS`, which holds recognition and
rerank. A reranker returns scores and reaches this gateway through
`/v1/rerank`. A chat request that names one gets a routing refusal the reader
cannot act on.

The test is what the operation list holds apart from the silent ones, not
whether a silent one is in it. A model that reranks and also answers chat stays.

### The key form

`console/src/routes/keys.tsx` hardcoded the non-admin scope set. It gained
`rerank:write`. Reranking reads the caller's own documents rather than a stored
one, so it carries its own scope beside chat rather than riding on it.

## Tests

Ten tests. Seven in the console, three in Go.

`ModelsTable.test.tsx`, three:

- The operations column names what each model serves, and does not hand one
  model's operation to another.
- An operation this console has never seen renders under its catalog name.
- Two operations that end in the same word stay apart. That case fails against
  the last-word design above, which is the reason the guard exists.

`ModelDetail.test.tsx`, two: a rerank offering shows `$0.0025 / search` and
`1,000`, and a chat offering claims neither a search price nor a page one.

`ModelPicker.test.tsx`, one: the picker omits a rerank-only model and keeps a
model that reranks beside chat.

`modelFilter.test.ts`, one: the operation facet selects by what a model serves.
A model whose offerings name no operation answers to none of them.

`internal/catalog/view/search_unit_price_test.go`, three:

- An offering priced only by search unit still reports a price. This is the
  fail-before for the projection half.
- The basis decides which price the projection reports. A token-billed offering
  with a figure beside it reports none. An offering that states no basis reports
  no price at all.
- The document limit travels with the offering. The fixture reads the shipped
  Cohere generation rather than a hand-built one, so the projection cannot drift
  from the response. An Anthropic offering states no limit.

`models_test.go` already guarded the OpenRouter offering shape against the
catalog projection by reflection. Both new members joined its fixture, and the
guard failed until `openRouterOffering` copied them.

## Verification

| Check | Result |
| --- | --- |
| `pnpm --dir console check` | build, `tsc --noEmit`, 151 tests passed |
| `bash scripts/verify-reranking.sh` | 20 passed, 2 failed |
| `bash scripts/verify-console-modernization.sh` | 21 passed, 0 failed |
| Nineteen gates in the required-evidence list | all exit 0 |
| `bash scripts/verify-starmap-ownership.sh` | exit 0 |
| `bash scripts/benchmark-overhead.sh` | exit 0 |
| `go test ./...` | exit 0 |
| `go vet ./...` | exit 0 |
| `make lint` | 0 issues |

The two failures are RNK-V19 and RNK-V20, which task RNK10 owns.
