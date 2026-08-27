# AMJ3 The Starmap video operation

A provider that generates a video answers with a job rather than a video. Before
this task nothing in the catalog named that route, so thirteen video-output
offerings published no operation at all. A caller had no route to request and no
price field to read.

## What gained the operation

| Fact | Value |
| --- | --- |
| offerings that gained `videos-generations` | 13 |
| providers that serve it | 1, deepinfra |
| resolved endpoint | `https://api.deepinfra.com/v1/openai/videos` |
| video-output offerings that still carry no operation | 0 |
| offerings that still carry no operation at all | 3, and each serves a realtime session |

The test `TestTheResidualOfferingsAreRealtimeAlone` holds the last two rows.
MOD0 counted 63 offerings with no operation, and MOD12 left 16. This task takes
the count to three. Each remaining one answers text and audio together, which is
the realtime shape no plan covers yet.

## One table decides two questions

Starmap already read a media operation table for two separate answers: which
operation an offering publishes, and which model facts a published operation
demands. Adding the video operation is therefore one constant and one table
entry.

```
Operation: ProviderOperationVideosGenerations
Tags:      video-gen, text-to-video
Input:     text
Output:    video
```

The derivation reads no price. A model that reports no price still publishes the
operation, and six of the thirteen do exactly that.

`validateModelFactConsistency` carried a hand-written `video-gen` case. It
required video output to be present. The table rule requires the output set to
be exactly `[video]` and the input to contain `text`, so it refuses everything
the old case refused and more. This task deletes the hand-written case. One
statement now decides both what Starmap publishes and what it refuses.

## The price reached the wrong field

DeepInfra reports one `output_seconds` price whatever the model produced. The
acquisition path recorded that number under `audio_gen` with no test of the
model. `video_gen` existed in the schema and nothing ever wrote to it.

AMJ7 prices a terminal job from these fields. A video price under `audio_gen`
answers a reader of the audio field and hides from a reader of the video field.
Both readers look correct.

`applyGeneratedSecondPrice` now keys the field on the declared output modality.
The order inside the client makes that safe:

1. `applyProviderFeatures` records what the provider reported.
2. `normalizeOperationalModalities` settles the modalities.
3. `applyProviderPricing` runs, and reads a settled answer.

Three cases hold the routing:
`TestAPerSecondPriceLandsUnderTheOperationTheOutputNames` covers video output,
audio output, and an undeclared output that still falls to audio. A second test,
`TestAnExistingPerSecondPriceSurvivesTheProviderAnswer`, keeps the acquisition
path from overwriting a price an authoritative source already recorded. Every
other field in that function defers the same way.

Seven of the thirteen persisted model files carried a price under `audio_gen`.
This task moves them, so the shipped catalog is correct today rather than after
the next refresh. The other six report no price.

## The Starport side

`internal/routing` gains `OperationVideosGenerations` and the served set gains
its eighth member. No transport descriptor declares the operation yet, so no
provider can plan it. AMJ4 adds the interface, and the registry refuses a
descriptor that claims the operation without one.

The package `internal/catalog` pins the spelling. The seam that hands a catalog
operation to the planner casts the string with no lookup. The planner treats a
name it does not serve as inert. A rename would therefore remove video from
routing with no error anywhere.

## Fail-before

Before this task the gate reported `Summary: 4 passed, 14 failed`. `AMJ-V05` and
`AMJ-V06` both failed, because neither the catalog projection nor the operation
set named the video operation.

## Acceptance

| Claim | Result |
| --- | --- |
| the gate moves by exactly two conditions | `Summary: 6 passed, 12 failed` |
| `AMJ-V05` and `AMJ-V06` pass | both report `PASS` |
| a Starmap test cross-checks the published operation | `TestEveryPublishedMediaOperationMatchesItsDefinition` expects 13 |
| Starmap ownership holds | `verify-starmap-ownership.sh` passes |
| the catalog still drives the providers | `verify-catalog-driven-providers.sh` passes |

## Verification

- In Starmap, `go test ./...` passes and `make lint` reports 0 issues.
- In Starmap, `verify-catalog-dependency-direction.sh` reports 8 passed, 0 failed.
- In Starmap, `verify-catalog-package-ownership.sh` reports 13 passed, 0 failed.
- In Starmap, `verify-consumer-deps.sh` passes for all six consumer modules.
- In Starmap, `verify-provider-fixture-drift.sh` is `UNVERIFIED`. It needs
  catalog-acquisition credentials that this machine does not hold.
- In Starport, `go test ./...` passes.
- In Starport, `bash scripts/verify-starmap-ownership.sh` passes.
- In Starport, `bash scripts/verify-catalog-driven-providers.sh` passes.
- In Starport, `bash scripts/verify-model-modalities.sh` reports 26 passed, 0 failed.
- In Starport, `bash scripts/verify-async-media-jobs.sh` reports 6 passed, 12 failed.
