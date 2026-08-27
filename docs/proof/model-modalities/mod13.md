# MOD13 one operation set, media routing, and the narrow interfaces

MOD13 is the task that makes the five media operations reachable. MOD12 taught
Starmap to publish them. This task teaches Starport to plan them, and it does so
in an order that keeps the gateway working at every commit.

## Three guards asked the same question and answered it separately

The gateway asks whether an operation is legal in three places.

| Guard | File | Question |
| --- | --- | --- |
| Request validation | `internal/routing/planner.go` | May a caller ask for this? |
| Snapshot validation | `internal/routing/planner.go` | May a catalog fact reach a route? |
| Descriptor validation | `internal/providers/connectors/transport_registry.go` | May a compiled transport declare this? |

Each held its own pair of hardcoded names. Widen one and miss another, and the
build publishes an operation it cannot plan, or refuses one it can.

`internal/routing` now owns `OperationSet` and the seven names, and all three
guards read it. Routing is the one package the other two import without a cycle.
It is also where the planner already decides what a route means.

## A catalog operation this build does not serve is inert

The snapshot guard used to reject the whole snapshot when it met an operation it
did not name. A catalog generation is data the build did not ship with. That
answer therefore removed chat and embeddings routing for every provider in the
generation, over one unknown name.

The guard now skips the route and keeps the rest. Caller input still fails
closed in request validation. This is what decision MOD-D6 states: catalog data
degrades, and caller input fails closed.

Two tests hold the pair. One puts a route carrying `video-generations` beside a
chat route and requires the chat request to route. The other asks for
`video-generations` directly and requires `ErrInvalidRequest`.

## The pin moved after the guards, not before

The plan orders the raise this way for a reason MOD12 measured.
`internal/providers.Assess` intersects every offering operation with what the
compiled transports support. The raised pin alone therefore carried no media
operation into a route. The hazard fires when a compiled transport declares one,
which is step 5 of this task. The guards widened first, then the transports
declared.

## Connector gained no method

The three media interfaces are one method each and optional.

| Interface | Operations |
| --- | --- |
| `ImageGenerator` | `images-generations`, `images-edits` |
| `SpeechSynthesizer` | `audio-speech` |
| `Transcriber` | `audio-transcriptions`, `audio-translations` |

A media method on `Connector` would force every transport to answer a call it
cannot serve. To the compiler, a stub that returns "unsupported" reads exactly
like a transport that does the work. The type system would then stop reporting
the difference. A test asserts that `Connector` still carries exactly `Chat`,
`ChatStream`, `Close`, `Embeddings`, and `Name`.

A descriptor that declares a media operation without its interface now fails at
startup. Without that guard the descriptor states the operation and the catalog
publishes it. A route then reaches it, and each caller learns at request time
that no code serves it.

## The probe reads the transport the route selected

One provider connector spans protocols. Only the OpenAI protocol carries a
compiled media transport. A probe of the composed connector would therefore
report a capability the selected protocol lacks.

`providerConnector` exposes one transport by endpoint type.
`SpeechSynthesizerFor` and its two siblings read that one. A test asserts that
the same connector answers yes for the OpenAI endpoint type and no for the
Anthropic one.

## One provider rejection produces one class

Every media method sends through the same error handler as chat. One test drives
a 429, a 401, and a 503 from one test server through four paths: chat, images,
speech, and transcription. It requires the same kind, the same retryability, and
the same state scope from all four. A media path with its own error vocabulary
would give the retry budget and the availability state two answers for one
rejection.

## What the projection carries

For each operation the catalog projection test counts the offerings the
generation publishes with a usable endpoint. It then requires the route set to
carry exactly that many.

| Operation | Routes |
| --- | --- |
| `chat-completions` | 512 |
| `embeddings` | 38 |
| `images-generations` | 25 |
| `images-edits` | 0 |
| `audio-speech` | 14 |
| `audio-transcriptions` | 7 |
| `audio-translations` | 7 |

The zero for `images-edits` matches the MOD12 census. Starmap's generation
publishes no image edit offering yet. The transport implements the operation
against the day one arrives.

The same test pins the wire spelling of all seven names. The seam that hands a
catalog operation to the planner casts the string with no lookup. A rename in
Starmap would put the value outside the served set. The new degrade rule would
then remove those routes with no error anywhere.

## In-seam repair

`internal/registry.Register` derived operations and endpoint types from the
catalog offerings alone, with no transport filter. It therefore published
operations no compiled transport implements. A search over `internal` and `cmd`
found no production caller. `providers.Assess` is the one seam that intersects a
catalog offering with what the build can execute. This task deletes the second
derivation rather than repairing it. Two derivations of one fact is the defect.

## Verification

`scripts/verify-model-modalities.sh` reports 20 passed and 6 failed. Conditions
MMD-V17, MMD-V19, and MMD-V20 pass. The six that remain are MMD-V21 through
MMD-V26, which MOD14 through MOD17 own.

The rest of the evidence:

- `go test ./...` exits 0, `go vet ./...` is clean, `make lint` reports 0
  issues, and `make build` succeeds.
- The pre-PR gate list in `CLAUDE.md` passes in full.
- `verify-starmap-ownership.sh` reports 12 passed and 0 failed.
- `verify-catalog-driven-providers.sh` reports 19 passed and 0 failed.
- `autoreview --gate pre-pr --mode auto` reports a clean review with 0
  findings, from `gpt-5.6-sol` at high effort.
