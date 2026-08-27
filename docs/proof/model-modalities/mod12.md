# MOD12 five media operations in Starmap

Conditions MMD-V17 and MMD-V18. The work lands in Starmap. This file records
what it changed and what it left.

## The census

MOD0 counted 613 shipped offerings, and 63 of them declared no operation.
Starport excluded every one as `operation_unsupported`, so none reached
`/v1/models`.

The five new operations claim 47 of the 63. Sixteen remain.

| Operation | Offerings |
| --- | --- |
| `images-generations` | 26 |
| `images-edits` | 0 |
| `audio-speech` | 14 |
| `audio-transcriptions` | 7 |
| `audio-translations` | 7 |

The image and audio counts overlap by design. A transcriber serves both audio
paths, because no model fact tells transcription apart from translation. The
54 rows above therefore cover 47 distinct offerings.

`images-edits` claims nothing today. No shipped model reads an image and
answers with an image alone. The operation still exists. A provider serves the
edit path and the generation path from one model, and a model that declares
that shape reaches both.

## The sixteen residuals

Thirteen offerings generate video. Decision MOD-D2 names five operations, and
none of them is video generation. A video path needs its own request shape, its
own response shape, and usually its own polling contract.

Three offerings serve a realtime session. Their output is text and audio
together over a socket. That is neither a chat completion nor one of the five
paths. `google-ai-studio/gemini-3.1-flash-live-preview`,
`google-ai-studio/gemini-3.5-live-translate-preview`, and
`openai/gpt-realtime-2.1` are the three.

`TestTheResidualOfferingsAreVideoAndRealtime` holds both numbers. A new
residual of any other shape fails the suite rather than passing unnoticed.

## What decides an operation

The derivation reads two facts: the declared modalities and the model tags. It
reads no price. `TestMediaOperationsIgnorePrice` pins that rule, and Starmap
pull request 102 established it.

The discriminator is the exact output set, not a membership test. Eleven
shipped offerings declare image output beside text output, and seven declare
audio output beside text output. Every one of them serves chat completions. A
model that also writes text answers through chat. A "contains image" rule
therefore gives those eleven an images path they do not serve.

Transcription is the one shape the modalities cannot name alone. Audio in and
text out is also the shape of a chat model that hears. One shipped offering has
exactly that shape with a `chat` tag. Transcription therefore requires the
`stt` tag. The seven offerings that carry it are the seven this task claims.

## One table decides both publication and refusal

`pkg/catalogs/media_operations.go` holds `mediaOperationFacts`. The derivation
reads it to publish an operation. The fact-consistency rule reads the same
table to refuse a model whose tag contradicts the modalities beside it.

The rule replaced three inline cases that checked `stt`, `tts`, and `image-gen`
separately. A survey of the shipped catalog confirmed that every media-tagged
model already satisfies the stricter rule, so the change breaks no existing
data.

## Endpoints

The five operations reach three providers.

| Provider | Paths added |
| --- | --- |
| OpenAI | all five |
| DeepInfra | all five, plus `embeddings` |
| Groq | the three audio paths |

Each path answered a probe with 401 rather than 404, which proves it exists.

The DeepInfra `embeddings` path is a repair this task revealed rather than
work it planned. Twenty-four DeepInfra embedding offerings declared the
`embeddings` operation and resolved to no route, because the provider file
listed no path for it. Adding the path closed all twenty-four.

Endpoint coverage moved from 526 resolved operations with 24 unrouted, to 579
resolved with one unrouted.

## The one endpoint this task did not add

Fireworks serves one image-generation offering, and it gets no path.

Its documented URL is
`https://api.fireworks.ai/inference/v1/workflows/accounts/fireworks/models/flux-1-schnell-fp8/text_to_image`.
An unauthenticated probe answers 404. Every path this task did add answers
401. The shape is also not the OpenAI one, and no `EndpointType` constant
describes the Flumina workflow protocol.

A wrong protocol fact is worse than a missing one. Starport would route to it
and fail at the wire. The offering therefore declares the operation and
publishes no route, which is the state the other 24 embedding offerings held
before this task.

## The pin stays where it is, and a probe corrected the reason

Step 6 of this task says to regenerate and release, and to leave the Starport
dependency pinned. Condition MMD-V17 reads Starport's `internal/catalog`, and
only the raised pin makes that question answerable. The condition therefore
moves to MOD13, which raises the pin. A gate that names a seam one task cannot
reach belongs to the task that can.

An earlier audit predicted a stronger reason, and a probe refuted it. The audit
expected a raised pin to hand `audio-speech` to the Groq adapter, reach a route,
and make `validateSnapshot` in `internal/routing` reject the whole snapshot.

The production path does not do that. `internal/providers.Assess` intersects
every offering operation with `TransportRegistry.Supports` before the operation
reaches adapter availability. At `v0.10.0` the composed routes carry 512
`chat-completions` and 38 `embeddings`, and no media operation. The full
Starport suite is green at the raised pin.

The hazard is real in shape and latent in time. It fires the moment a compiled
transport declares a media operation, which is what MOD13 step 5 does. MOD13
therefore widens the three guards before it declares a media transport, and
that ordering is what decision MOD-D6 states.

A first probe reached the opposite conclusion by modelling `Registry.Register`,
which recomputes operations from the catalog offerings with no transport
filter. That method has no production caller. MOD13 owns the repair.

## Verification

In Starmap, `GOTOOLCHAIN=go1.25.12 go test ./...` is clean, `make lint`
reports 0 issues, and `make verify` passes.

`TestEveryPublishedMediaOperationMatchesItsDefinition` walks every shipped
offering. For each published media operation it asserts three things.

- The model satisfies the canonical definition.
- The offering publishes no chat route beside it.
- The per-operation count matches the census above.

`TestEmbeddedOfferingsDoNotPublishChatRoutesForNonChatOperations` grew
stronger rather than weaker. It asserted that three non-chat models publish no
endpoint at all. That assertion held only while those models published nothing.
It now pins the exact operation and URL each one publishes, and it still
refuses a chat route.

The pinned-artifact consumer fixture carries a new archive digest, because the
embedded catalog changed. Go 1.25.12 produces
`6d3e9cec80c0624bfc399e9bc907424892d104fe4f99fa5462cd01620b226d99`. The digest
covers gzip output, so it moves with the toolchain, and CI pins 1.25.12.

In Starport, `bash scripts/verify-starmap-ownership.sh` and
`bash scripts/verify-catalog-driven-providers.sh` pass.
