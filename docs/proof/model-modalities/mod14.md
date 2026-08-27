# MOD14 media types, routes, and scopes

MOD13 taught the gateway to plan the five media operations. MOD14 gives a caller
a way to reach them. This task adds the canonical media types, the eight paths
the two protocol families publish, and the two scopes that guard them.

## What a caller can now call

| Path | Protocol | Operation | Scope |
| --- | --- | --- | --- |
| `POST /v1/images/generations` | OpenAI | `images-generations` | `images:write` |
| `POST /v1/images/edits` | OpenAI | `images-edits` | `images:write` |
| `POST /v1/audio/speech` | OpenAI | `audio-speech` | `audio:write` |
| `POST /v1/audio/transcriptions` | OpenAI | `audio-transcriptions` | `audio:write` |
| `POST /v1/audio/translations` | OpenAI | `audio-translations` | `audio:write` |
| `POST /api/v1/images` | OpenRouter | `images-generations` | `images:write` |
| `POST /api/v1/audio/speech` | OpenRouter | `audio-speech` | `audio:write` |
| `POST /api/v1/audio/transcriptions` | OpenRouter | `audio-transcriptions` | `audio:write` |

OpenRouter publishes no image edit path and no translation path. MOD0 read that
from its own OpenAPI document. The two operations therefore reach the gateway on
the OpenAI family alone. The OpenRouter list stays short instead of gaining
paths its own clients cannot call.

## One operation set, written over a type parameter

A media request crosses five seams: the codec, the controller, the proxy, the
router, and the connector. Three operations across five seams give fifteen types
when each one carries its own name.

The gateway writes each seam once over a type parameter instead:

- `router.MediaRequest[R]` and `router.MediaResponse[R]`, with six aliases.
- `proxy.MediaRequest[R]` and `proxy.MediaResponse[R]`, with six aliases.
- One `routeMedia`, one `processMedia`, one `captureMedia`, one
  `mediaGatewayRequest`.

The reason is the retry budget. Three copies of a route plan, a credential
policy, and an attempt budget drift into three policies. An operator then debugs
which of the three a failing call took.

## The operation comes from the request, not from a flag

Two of the five operations cannot be told apart by their bodies:

- An image edit carries a source image. `ImagesRequest.IsEdit` reads that, and
  the router plans `images-edits` when it answers yes.
- A translation and a transcription send identical bodies. Only the path differs,
  so the controller marks `TranscriptionRequest.Translate` and the router plans
  `audio-translations`.

Both facts reach the planner as data on the canonical request. A caller cannot
send one and reach the other.

## The gateway holds an upload as bytes

A route plan retries across providers. Each attempt builds its own provider
request from the canonical one. A reader that the first attempt drained would
reach the second empty, and the caller would read the last provider's rejection
instead of an answer.

The canonical `UploadedFile` therefore holds three facts:

- the decoded bytes,
- the filename,
- the media type the caller stated.

`Clone` copies the bytes into their own array, so one attempt cannot corrupt the
next.

The gateway bounds the body before it reads the parts. A media request stops at
64 MiB. A multipart body keeps 8 MiB in memory, then spills the rest to a
temporary file. An unbounded body is an unbounded allocation.

## A media answer is never cached

`proxy.MediaResponse` carries no cache fields, and the caching layer passes all
three operations straight through. An image and an audio file are large, and a
caller that repeats a prompt expects a new rendering rather than the previous
one.

## Two new seams

**`internal/protocol/mediaform`** reads a media request that arrived as
multipart form data. An image edit and an audio transcription carry a file, so
neither can arrive as JSON. Both protocol families spell the parts the same way,
and the two codec packages do not import each other. One shared reader keeps one
contract for one wire format. The architecture test pins it as a leaf beside the
canonical vocabulary.

**The connector media target** is the route binding every media provider request
carries. It holds the provider's own model ID, the endpoint the planner chose,
and the credential that pays for the call. The three request types held those
same three fields separately before this task. One named concept also fixed a
defect: the image request marshaled its endpoint into the provider body, because
that one field carried no `json:"-"` tag.

## An anonymous deployment reaches the media routes

An operator who runs with authentication disabled has no key. The anonymous
identity now carries `images:write` and `audio:write` beside the scopes it
already held. Without them, the mode that exists to make the first request work
would refuse half the surface. The console applies the same default when it
mints a non-admin key.

## The route test replaced the route grep

Condition MMD-V21 read the route source for the five absolute OpenAI paths. The
server registers each path relative to its protocol group. A path a caller types
therefore never appears in the source that registers it. The same grep would
also read a doc comment as a route.

The condition now holds a route test. It walks the router the server built and
requires each of the eight paths. A path registered under the wrong group reads
as present to a source scan and returns 404 to a caller. The walk catches that.

## Acceptance

| Condition | Test |
| --- | --- |
| MMD-V21 | `TestServerRegistersTheMediaPaths` walks the built router |
| MMD-V22 | `TestMediaRoutesCarryTheirScopes` and `TestAnonymousDeploymentReachesTheMediaRoutes` |
| MMD-V23 | `TestTranscriptionUploadReachesTheCanonicalRequest` |

The scope test states the whole access rule. A key holding chat access alone
reads 403 on all eight paths, and a key holding the media scopes reaches the
controller on all eight. The second half matters as much as the first, because a
scope no key can satisfy also refuses every caller.

Three more tests hold the rest:

- `TestUploadedAudioReplaysAcrossAttempts` requires three clones of one upload
  to carry the same bytes and filename. Each clone owns its own array.
- `TestTranslationPathMarksTheCanonicalRequest` requires the translation path to
  mark the canonical request.
- `TestEncodeImagesHoldsTheOpenAIShape` and its OpenRouter twin pin the image
  answer of each protocol family, including the two fields that separate them.

## In-seam repair

The three router attempt closures shared one shape. Only three identifiers
differed. One media call type now names those three steps, and one attempt
method writes the failure handling around them. All three operations report a
missing transport, a provider error, and an unreadable answer the same way.

## Verification

`scripts/verify-model-modalities.sh` reports 23 passed and 3 failed. Conditions
MMD-V21, MMD-V22, and MMD-V23 pass. The three that remain are MMD-V24, which
MOD15 owns, and MMD-V25 and MMD-V26, which MOD16 owns.

The rest of the evidence:

- `go test ./...` exits 0, `go vet ./...` is clean, `make lint` reports 0
  issues, and `make build` succeeds.
- The pre-PR gate list in `CLAUDE.md` passes in full.
- `verify-starmap-ownership.sh` reports 12 passed and 0 failed.
- `verify-catalog-driven-providers.sh` reports 19 passed and 0 failed.
- `verify-openrouter-parity.sh` reports 16 passed and 0 failed.
- `verify-auth-onboarding.sh` reports 26 passed and 0 failed.
- `smoke-openrouter-sdks.sh` passes the Python, TypeScript, and Go SDK checks.
- `benchmark-overhead.sh` reports p50 0 ms and p99 0 ms over 200 requests.
