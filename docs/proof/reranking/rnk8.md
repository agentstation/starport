# RNK8 proof: search units in usage, the price, and the spend budget

Task RNK8 of the reranking plan. Conditions RNK-V15 to RNK-V17.

## Problem

The usage record counted prompt and completion tokens. A rerank turn on Cohere
produces neither, so the record reported zero and the turn read as free. An
account with a spend budget could rerank without limit, because a budget holds
only when the gateway knows the cost.

## What landed

### The record field

`usage.Record` gained `SearchUnits int64`. It sits beside `Media` rather than
inside it, because reranking produces no media, and outside `Tokens` because no
token total converts into it. A Cohere turn reports a search unit and no tokens.

Usage records store as JSON blobs, so an optional field needs no schema bump.

### The price

Providers disagree on the unit they bill. Cohere bills a search unit, which is
one query against a bounded document count. Voyage bills the tokens it reads.
The offering states its own basis, so `billableRerank` in
`internal/catalog/control_plane.go` reads the basis first and then the price
that basis names:

- `ModelRerankBasisSearchUnit` reads `Pricing.Operations.SearchUnit`.
- `ModelRerankBasisToken` reads `Pricing.Tokens.Input`.
- An offering that names no basis states no rerank price, even beside a token
  price. The basis field exists so a consumer reads the right price instead of
  guessing from whichever one is present.

An offering that names a basis and publishes no price for it returns
`ErrRerankUnpriced`. That reaches planning through `billableOperation`, which
document recognition already used, so the offering loses its rerank operation
through `RouteExclusionOperationUnpriced` and keeps every other operation. This
is decision RNK-D7 held before planning rather than after accounting.

A stated zero is a decision and stays billable. A missing price is a gap.

### The meter

`searchUnitCost` in `internal/proxy/usage_capture.go` multiplies the count the
provider reported by the offering price. A turn that reported no search unit
costs nothing, whether it reranked nothing or ran on a token-billed offering.
A turn that reported one on an offering with no search unit price records
`CostReasonRerankUnpriced` rather than a zero cost. That reason names a snapshot
that reached accounting without the projection guard above.

`usageCost` now takes the record rather than five loose parameters, which is
what let one more billing unit join without a sixth.

Step four of the task needed no code. `usage.Totals.SpendNanoUSD` already
aggregates `record.Cost.NanoUSD`, and `internal/server/budget.go` already maps
`limits.DimensionSpend` onto that total.

### The bound

`affordableRerank` in `internal/proxy/rerank.go` refuses a turn the account
cannot pay for, before the provider call. It follows the document reader:

- One search unit is the floor. Every rerank call on a search-unit offering
  bills at least one. A call over the provider's per-unit document count bills
  several.
- `RoutableSnapshot.LowestSearchUnitPrice` answers the cheapest offering of the
  requested model. The planner has not chosen yet, so no exact price exists.
- The estimate errs low. A bound that refused affordable work would cost a
  caller a request over a price never charged. Erring low overshoots by one
  call, and the account's own cap refuses the next one.
- A token-billed offering states no floor before the provider reads the
  documents, so it estimates nothing and refuses nothing.
- An unbounded account, which is most deployments, is never consulted.

The refusal is `failure.Billing`, which wraps `limits.ErrSpendLimitExceeded`.
The message names the model, which is the one thing the caller can change.

### The wire

`RerankUsage` in `internal/protocol/openrouter/rerank.go` gained
`Cost *float64`, which the OpenRouter schema states on every rerank answer. The
figure is the gateway's own accounting, because a rerank provider reports units
and no money at all.

The accounting middleware already prices the turn, so it writes the result back
onto `OperationResponse.Cost` and the controller converts nano-USD to dollars
at the wire boundary. The caller reads the same number the gateway bills,
because both come from one derivation rather than from pricing the turn twice.
A turn nothing priced omits the member rather than reporting zero, because a
caller reading zero would take it for a free request.

`Cost` did not join `inference.Usage`, which reports what the provider metered.

## Seams, and why they moved

Decision RNK-D19 records the amendment. RNK8 named `internal/usage`,
`internal/limits`, and `internal/catalog`.

The record field landed in `internal/usage`. The meter that fills it reads the catalog snapshot the router
returned. The bound that refuses a turn reads the allowance the request carries.
Both live where the request is. A test in `internal/usage`
would hold an arithmetic no request runs through.

Conditions RNK-V15 to RNK-V17 name the seams the code uses.

## Tests

Thirteen tests. Two properties shape them. The fixtures read the shipped
catalog, so they cannot drift from the real projection. The counts are never
one, so a wrong multiplier still fails the test.

`internal/catalog/rerank_price_test.go`, seven tests:

- `TestTheBasisDecidesWhichRerankPriceIsRead`, a seven-case table. Both bases
  priced correctly pass. A search-unit basis priced only in tokens fails, and a
  token basis priced only in search units fails. No basis beside a token price
  fails. A negative search unit fails. No prices at all fails.
- `TestAFreeRerankOfferingIsBillable`, which separates a stated zero from a
  missing price.
- `TestAnUnpricedRerankOfferingLosesOnlyTheRerankOperation`, which asserts that
  `compatibleOfferingService` drops rerank and keeps chat.
- `TestTheShippedCatalogPricesEveryRerankRoute`, a census over the shipped
  generation: three search-unit offerings, two token ones, each with a positive
  document limit.
- `TestTheLowestSearchUnitPriceIsTheCheapestOfferingOfThatModel`, which reads
  0.001 over 0.0025 and answers to the provider-qualified name as well.
- `TestATokenBilledRerankOfferingStatesNoFloor`, which covers a token basis, a
  model no generation holds, and a nil receiver.

`internal/proxy/rerank_accounting_test.go`, four tests:

- The fail-before is `TestARerankTurnRecordsItsSearchUnitsAndItsCost`. It asks
  for two units, then asserts a cost of two times the offering price. It also
  asserts an empty cost reason, and a response cost equal to the record cost.
- Three search units run through a token-billed route in
  `TestASearchUnitOnAnUnpricedOfferingIsNeverBilledAtZero`. That case asserts a
  nil cost with `CostReasonRerankUnpriced`. The same turn on the search-unit
  route carries a cost, so the reason names the offering, not the operation.
- The bound has its own case,
  `TestATenantAtItsSpendBoundIsRefusedBeforeTheProviderCall`. At 2,499,999
  nano-USD it refuses without reaching the router. At 2,500,000 nano-USD it calls
  once. An earlier fixture registered one provider's adapter. That
  made the unpriced case pass for the wrong reason: the route did not resolve.
  The fixture now registers every rerank provider.
- `TestAnUnboundedAccountAndATokenBilledModelRefuseNothing` states where the
  estimate stops.

`internal/protocol/openrouter/rerank_test.go`, two additions: a priced answer
carrying `cost`, and a nil-cost answer whose JSON contains no `cost` member at
all.

## Verification

| Check | Result |
| --- | --- |
| `bash scripts/verify-reranking.sh` | 19 passed, 3 failed |
| Nineteen gates in the required-evidence list | all exit 0 |
| `bash scripts/benchmark-overhead.sh` | exit 0, p50 0ms, p99 0ms |
| `go test ./...` | exit 0 |
| `go vet ./...` | exit 0 |
| `make lint` | 0 issues |

The three failures are RNK-V18, RNK-V19, and RNK-V20, which tasks RNK9 and
RNK10 own.
