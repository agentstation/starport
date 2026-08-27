# MOD9 output modalities on the canonical chat types

Condition MMD-V12.

## The task was smaller than the plan says

The plan text asks for a `Modality` type. MOD4 already shipped one at
`internal/inference/types.go:56`, together with the five constants and the
`pdf` translation the catalog boundary owns.

MOD9 therefore adds four things: `ChatRequest.OutputModalities`,
`ChatRequest.AudioOutput`, `ChoiceDelta.Audio`, and the clone coverage that the
last of those makes necessary.

## The condition held before its own task ran

MMD-V12 asked for `type Modality` and `Audio`. Phase A shipped `ContentAudio`,
and MOD4 shipped `Modality`. A grep for both terms therefore passed against a
tree where MOD9 had not started.

The condition now names the symbols this task adds, and requires the clone
sweep beside them. Against the tree at `b53dccf` it reports FAIL, and the gate
summary drops from 12 passed to 11.

## The aliasing hazard the audio chunk creates

`ChoiceDelta` carried no pointer field before `Audio`. `StreamEvent.Clone`
copied each delta by struct assignment, and every field came along by value.

`AudioChunk` owns a byte slice. A retried attempt hands two readers the same
backing array, and so does a replayed cached stream.

The clone methods are hand-written, one line per field that owns memory. A
field added without its line compiles and passes every existing test. It
aliases in production under retry or replay alone.

`output_test.go` therefore names no fields. It fills each canonical type by
reflection, clones it, and walks both sides for shared memory.

## The cache key moved, and the version says so

`ChatKey` hashes a payload that embeds `inference.ChatRequest` whole.
`assertNoTransportTags` forbids every struct tag on a canonical type, so
`json:",omitempty"` is not available to hide an unset field. Both new fields
reach the hash as `null` on a request that never sets them.

The text-only key moved from
`47d183667d1611efe9fd3a340f8ad6f7f33375d5d07d76c00758e1c05eee5328` to
`d3f60e45512f5b5902b20c0db900e34befafd9fc6bf522e0e8edf74cddfd6247`.

`SemanticKeyVersion` rises from 2 to 3 in the same change. Stale entries keep
the prefix that wrote them, so no encoding reads an entry it did not write.
Starport has not released, and the repository prefers a direct breaking change
to a compatibility path.

The guard test changes with it. Its former name claimed that text-only keys do
not change. That claim cannot survive a field addition.

The test now pins the full key including the version, and states the rule. A
change may move this digest. It may not move it silently.

## Two normalizations that prevent a second flush

An OpenAI client sends `modalities: ["text"]` on an ordinary turn. MOD10 makes
the codec read that field. Without a rule, the field splits the cache in two on
the day MOD10 lands. Half a deployment then never reads the other half's
entries.

`ChatKey` therefore drops a text-only modality list, and sorts the rest. The
field names a set, so a caller that lists audio before text asks the question
the other order asked.

MOD9 handles MOD10's consequence at its own seam. That costs one version bump
instead of two.

## A second defect the pinned key cannot catch

The pinned key catches a field that reaches the key when it should not. The
expensive defect is the opposite one.

A field that never reaches the key lets two requests that want different
answers share one entry. The caller then receives a reply to a question it did
not ask.

Every field arrives today because one line embeds the struct. That is a
property of that line, not a promise.

`keyfields_test.go` walks `ChatRequest` by reflection. Each field must either
move the key under test, or carry a recorded reason for its exemption. Three
fields are exempt: `Stream` and `StreamOptions` are delivery format, and
`Extensions` makes the request ineligible before a key exists.

## Verification

`go test ./...` and `go vet ./...` are clean. `make lint` reports 0 issues.
`scripts/verify-model-modalities.sh` reports 12 passed, 14 failed, with
MMD-V12 passing.

Four mutations prove the new tests fail for their own reasons.

| Mutation | Failure |
| --- | --- |
| drop the `OutputModalities` clone line | `the source output modalities changed; the clone shares the slice`, plus the sweep at `ChatRequest.OutputModalities` |
| drop the delta `Audio` deep copy | `the source audio bytes changed; the clone shares the chunk's slice`, plus the sweep |
| drop the modality normalization | `asking for text alone produced a second key`, and `two spellings of one modality set produced two keys` |
| drop `AudioOutput` from the keyed-field table | `ChatRequest.AudioOutput reaches neither keyedFields nor exemptFromKey` |
