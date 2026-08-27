# MOD10 generated media through both codecs and the cached replay

Conditions MMD-V13 and MMD-V14.

## What a caller writes and what a caller reads

A request asks for a non-text answer with two fields. `modalities` lists what
the caller accepts, and `audio` names the voice and the container.

A response returns one with two more. `images` carries a finished picture
beside the content, and `audio` carries the bytes together with a transcript.

These are the OpenAI spellings. OpenRouter documents the same four, so both
codecs answer the same shape and a caller that switches routes changes only
the path.

## The transcript travels beside the bytes

A spoken answer reaches the canonical types as two parts. One is text holding
the transcript, and one is audio holding the bytes.

Neither codec nulls the content field when it fills the audio field. A caller
that cannot play audio still receives the answer in words.

## A delta needs two fields, not one

An image arrives whole. A provider sends the finished picture in a single
delta, so there is nothing to accumulate.

Audio arrives in pieces. A spoken answer streams, and the base64 runs
concatenate.

`ChoiceDelta` therefore carries `Media` for the first case and `Audio` for the
second. One field could not serve both without a rule that tells a reader
which of the two it holds.

## The cached replay refused the answer it stored

`StreamEvents` rebuilds a stream from a stored response. It refused every part
that was not text.

A caller who asked for a picture therefore received it while the cache missed,
and received an error once the cache hit. The failure appeared only under
load, because only a repeated request reaches a hit.

Replay now splits a completed message. Text accumulates into the delta text,
and every other part rides the delta media field.

One case still fails. A part that claims to be text while it carries a
picture contradicts itself. No reader resolves it without a guess about which
half to believe.

## Two defects the media path exposed

`appendMessageText` wrote into `Content[0]` without reading it. A picture
arriving before the first text delta therefore received the answer's words
inside the image part.

The mutation that restores the old code shows the result directly: one part,
of kind image, holding the text `here it is`.

`streamEventKind` read text, reasoning, and tool calls. A chunk that carried
the finished picture beside the assistant role therefore matched the start
shape.

That shape costs the answer. A start event announces the turn and carries
nothing a consumer must read.

This task fixes both rather than files them. The field this task adds is the
only route to either one.

## Two conditions that held too loosely to catch their own work

MMD-V13 asked for the exact tag `json:"modalities"`. The shipped tag carries
`omitempty`, so the condition could not match it.

It also passed two codec paths to `all_present`, which ORs its paths. One
codec carrying the vocabulary satisfied a condition whose text names both.

MMD-V14 grepped a single codec for the lowercase word `images`. The Phase A
input path already carried that word.

A new helper, `in_each`, requires every term under every path. Both conditions
now use it, and both name the encoder rather than a word.

Against `HEAD` before this commit, no codec file contains any of the six terms
the two conditions now require.

## Verification

`go test ./...` and `go vet ./...` are clean. `make lint` reports 0 issues and
`make build` succeeds.

`bash scripts/smoke-openrouter-sdks.sh` reports PASS for the Python,
TypeScript, and Go OpenRouter SDKs.

`scripts/verify-model-modalities.sh` reports 14 passed, 12 failed, with
MMD-V13 and MMD-V14 passing.

Seven mutations prove the new tests fail for their own reasons.

| Mutation | Failure |
| --- | --- |
| drop `Modalities` from the adapter request | `TestOutputModalityRoundTripsThroughTheAdapter`, expected `["text","audio"]`, actual nil |
| drop the generated media fold from the adapter response | three tests: two content lists short by one part, and an undecodable payload that no longer errors |
| revert the start classification to text and tool calls | `TestAChunkCarryingMediaIsNotAStartEvent`, `should not be: "start"` |
| revert `appendMessageText` to index zero | `TestTextArrivesInItsOwnPartAfterMedia`, one part of kind image holding `here it is` |
| refuse a non-text part in replay again | both replay tests, unexpected error |
| drop `encodeGeneratedImages` from the chat encoder | `TestEncodeGeneratedImage`, empty images list |
| drop `decodeModalities` from the request decoder | `TestDecodeOutputModalityRequest`, expected `["text","audio"]`, actual nil |
