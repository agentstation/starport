# MOD11 media units in usage, cost, and the spend budget

Conditions MMD-V15 and MMD-V16.

## A token total cannot describe a picture

A provider meters a generated image per image. It reports no tokens for one,
so a cost that reads tokens alone returns zero for a turn that charged four
cents.

Zero is also the number a tenant spend budget subtracts. An unpriced media
turn was therefore unbounded rather than merely unbilled.

## Audio is a share, not an addition

A provider reports audio tokens inside the input and output totals, the way it
already reports a cache read. Adding the share on top bills the same audio
twice.

The cost path subtracts each share out of the plain rate and adds it back at
its own rate. Cache reads, cache writes, and both audio directions now follow
one rule.

The rates differ enough that the choice matters. A published audio input rate
runs many times the text rate for the same model.

## The whole cost drops, not the media half

An offering that prices tokens and prices no picture can still compute a token
cost. That number is real and it is not the bill.

The cost path therefore withholds the whole figure and names
`media_unpriced`. An operator reading the record sees a gap rather than a
number that understates by two orders of magnitude.

The same rule covers audio. Falling back to the text rate is not a rounding
error.

## The estimator counts what it can count

The plan asked for a count of each media kind in the token estimator. The
estimator now reads the one shared media walk rather than its own inline image
check, and it converts images alone.

No published rate turns a second of audio or a page of a document into tokens
across providers. A constant written here would enter a real charge and a real
spend budget as an invented number.

The estimator reports the gap instead. `EstimateMediaUnits` counts every kind,
and the cost path names the unit the offering does not price.

## What the shipped catalog cannot supply

Starmap v0.9.0 prices audio on no offering at all. The two fields MOD8 added
exist in the schema and carry no data.

It prices a generated image on 36 offerings. Every one of them declares no
operation, so none resolves to a route.

The derivation causes that. It reads a priced `image_gen` as proof that the
model is not a chat model. Version 0.9.0 then has no image operation to give
the offering instead.

That is the same residual the MOD12 census counts. Until MOD12 closes it, the
priced half of this rule has no catalog fact to read. The priced tests
therefore state the price themselves and say so in place.

## A streamed picture exists only as a running total

A streamed turn reports its usage on one event and its pictures on others. No
single event holds both.

The capture wrapper accumulates the count across deltas and writes it onto the
latched usage. That wrapper is the only reader that sees the whole stream.

## Verification

`go test ./...` and `go vet ./...` are clean. `make lint` reports 0 issues and
`make build` succeeds.

`pnpm --dir console check` passes: 19 test files, 115 tests.

`scripts/verify-model-modalities.sh` reports 16 passed, 10 failed, with
MMD-V15 and MMD-V16 passing.

`verify-v1-architecture.sh` reports 12 passed, `verify-dependency-direction.sh`
6 passed, and `verify-package-layout.sh` passes.

Seven mutations prove the new tests fail for their own reasons.

| Mutation | Failure |
| --- | --- |
| drop `record.Media` on the chat path | `TestAGeneratedImageAgainstATextPriceHasNoCost`, media absent |
| drop the media half from `usageCost` | that test plus `TestAnAudioTurnAgainstATextPriceHasNoCost`, a cost where a reason belongs |
| bill the audio share on top of the plain share | `TestAnAudioTurnAgainstAPricedOfferingCosts`, the same audio billed twice |
| drop the audio shares from `usageTokens` | both audio tests, no audio reaches the record |
| stop accumulating streamed pictures | `TestAStreamedPictureReachesTheRecord`, media absent |
| drop the image count in the adapter | `TestAGeneratedImageAgainstATextPriceHasNoCost`, media absent |
| drop the audio token read in `usageToInference` | `TestAnAudioTurnAgainstAPricedOfferingCosts`, audio never leaves the wire type |

## Two conditions that named the wrong seam

MMD-V15 looked for the cost reason under `internal/inference`. The cost-reason
vocabulary belongs to `internal/usage`, which owns the record that carries a
reason.

MMD-V16 asked for `MediaUnits` under `internal/limits`. `internal/inference`
already owns that word, and one package owns a word here. The condition
therefore asked for a copy rather than for its own text.

Its text says spend, and a spend budget reads the cost on a record. Both
conditions now assert what they say. One predicate each holds the declaration,
the use, and a test together.

## Observed, not fixed

The console usage page shows the media counts in its request detail panel and
translates the new reason. No console test covers either.

Both live inside a route component that exports nothing. A test would have to
export internals for its own sake, and it would then assert one map entry and
one conditional row.
