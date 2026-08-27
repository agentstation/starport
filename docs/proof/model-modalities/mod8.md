# MOD8 audio token prices

Condition MMD-V11. Starmap PR
[agentstation/starmap#103](https://github.com/agentstation/starmap/pull/103).

## The defect

models.dev quotes `input_audio` and `output_audio` in dollars per million
tokens, in the same units as `input` and `output`. The parser filed both under
`ModelOperationPricing`, whose `AudioInput` means a flat price for one audio
input. The catalog therefore claimed a $1.00 fee per audio file where a $1.00
per-million-token rate belongs.

Both concepts are real. 12 deepinfra offerings publish a genuine flat audio
price: Voxtral, Whisper, Veo, Wan, LTX, Pixverse, and Nemotron ASR. The fix
separates the two rather than replacing one with the other.

## The counts

The vendored models.dev snapshot records `input_audio` on **74** models and
`output_audio` on **18**, across **18** providers. Starmap tracks 12 of those
providers, so a regeneration prices audio on this many catalog models:

| Regeneration | `tokens.audio_input` | `tokens.audio_output` |
| --- | --- | --- |
| at `main`, without this change | 0 models | 0 models |
| with this change | 23 models | 3 models |

## The fact is lost today, not only misfiled

Three reads of `google-ai-studio/gemini-2.5-flash`:

| Tree | Audio price |
| --- | --- |
| committed catalog | `operations.audio_input: 1.0`, a flat fee |
| regenerated at `main` | absent |
| regenerated with this change | `tokens.audio_input.per_1m: 1.0` |

Commit `ce19e939` derives offering operations from the served operation rather
than the pricing shape. A regeneration at `main` therefore drops the operations
block that carried the audio price. The fact disappears. MOD11 cannot price an
audio turn from a catalog that does not record the rate.

## Starport reads the new prices

`OfferingPricingInfo` carries `audio_input` and `audio_output`, and
`offeringPricing` projects them.

`priceFields` and `tokenPrice` gain both names, so a change to an audio rate
reaches the operator through the freshness diff.

Both lists carry their entries by hand. A mismatch fails silently.

A name without a matching case resolves to absent on every diff.
`diffOfferings` then skips it. A real price change reads as no change.

`TestPriceFieldsCoverEveryStarmapTokenPrice` walks `ModelTokenPricing` by
reflection. It checks both directions. Every listed name must resolve to its
own field. `priceFields` must name every price Starmap records.

## Why no catalog regeneration ships here

The nightly Catalog Generation workflow owns that step. It runs with eight
provider API keys. A local run without them degrades provider-sourced facts to
models.dev values. That rolls OpenAI prices backward and attaches empty price
blocks to subscription providers. Both effects reproduce at `main` with no part
of this change applied, so they belong to the workflow, not to MOD8.

The counts above come from a local regeneration, read as a difference against
the same run at `main`. That difference cancels the degradation. Only the audio
relocation remains.

## Verification

In Starmap, `go test ./...` and `go vet ./...` are clean under Go 1.25.12.
`make verify` reports repository verification passed.

Starmap checks its generated godoc into the tree.

`make docs-check` runs gomarkdoc in check mode over every package that holds a
`generate.go`. A new struct field moves that output. The gate then fails until
someone regenerates the README.

MOD12 adds fields to the same package. It must regenerate again.

In Starport, `bash scripts/verify-starmap-ownership.sh` passed, and
`scripts/verify-model-modalities.sh` reports MMD-V11 passing.

`autoreview --gate pre-pr --mode auto` selected Sol at high effort and reported
no findings.

## Fail-before

| Mutation | Test that fails |
| --- | --- |
| drop the `audio_input` branch from `MarshalYAML` | the YAML round trip reports `AudioInput` nil |
| map `input_audio` to `Operations.AudioInput` again | the parser test reports operations pricing that should be nil |
| drop `audio_input` from `priceFields` | Starmap prices it and `priceFields` omits it |
| drop the `audio_input` case from `tokenPrice` | `tokenPrice` has no case for that entry |
| let the merger keep orphaned reasoning controls | the merged model carries levels beside denied support |

## Observed, not fixed

`pkg/catalogs/artifact.TestBundleReproducibleFixtureHashes` fails on Go 1.27.
It passes on Go 1.25.12.

The artifact is gzip-compressed. The golden checksum tracks the output of one
compressor. The failure reproduces at `main`, so it predates this work.

The test claims reproducibility. That claim holds for one toolchain, not for
the format.

A models.dev token price of exactly zero becomes a cost whose YAML form is an
empty map, because `ModelTokenCost.MarshalYAML` omits zero values. Every
example is a subscription provider that publishes no per-token rate: hetzner,
zai-coding-plan, alibaba-token-plan, and zhipuai-coding-plan. A regeneration at
`main` reproduces it. It is also what makes the Hetzner provider contract test
report unpublished pricing after a local regeneration.
