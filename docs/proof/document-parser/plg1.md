# PLG1 The plugin contract

## Outcome

`scripts/verify-document-parser.sh` moves from 0 of 20 to 3 of 20.
`PLG-V01`, `PLG-V02`, and `PLG-V03` pass. A caller that names the
`file-parser` plugin now gets a typed option on the canonical request. A caller
that names any other plugin, or any engine outside the two this gateway runs,
gets a refusal.

## What a caller used to get

The codec decoded `plugins` as raw JSON and dropped it. The gateway then
served the request, billed the caller, and reported the field in a response
header the caller had to know to read.

That is a wrong answer rather than a missing feature. A parser plugin changes
what the model reads. A request that attaches a scanned page and names an
engine asks a different question than the same request with the plugin
removed. Serving the second and returning the first one's header answers the
question the caller did not ask.

So an unenforced plugin identifier is now HTTP 400 at the route, and the
refusal names the one identifier this deployment does run.

## Why this gateway serves two engine names and not OpenRouter's three

OpenRouter states `mistral-ocr`, `cloudflare-ai`, and `native`. Two of those
name a vendor Starport cannot reach. The file `plg0.md` records the census. The Starmap
catalog serves zero models under `mistral`, and `cloudflare` is absent from
the catalog.

Accepting `mistral-ocr` and recognizing the page through some other vendor
reports work this deployment did not do, which is the unkept promise invariant
P2 forbids. Decision PLG-D4 chose refusal over a silent fallback.

The vocabulary is therefore `native` and `recognition`, and the refusal carries
both names. A caller that got one name wrong needs the whole vocabulary back
rather than the news that one name failed.
The test `TestAnUnknownEngineIsRefusedRatherThanIgnored` drives all three
OpenRouter vendor names plus a wrong-case `Native`. It asserts that the served
list appears in each refusal.

## The zero value is not the native engine

`inference.DocumentParser{}` means the caller asked for no extraction.
`Engine: native` means the caller asked for the in-process read.

Folding those two together looks harmless and is not. A request that attaches a
file and names no plugin leaves the document to whatever the chosen model does
with it. For a model that reads PDFs, that is the whole answer. A request that
names the native engine gets extracted text whether or not the model reads
documents. The test `TestNoPluginsIsNotTheNativeEngine` holds the split for an absent
field and for an empty array.

An entry that names `file-parser` and no engine does default to native. OpenRouter picks a default the same way. Native is
also the one engine that reaches no provider and costs nothing.

## Two refusals the plan did not ask for

A `plugins` array can hold two `file-parser` entries naming two engines.
Reading the first extracts the document a way the caller who wrote the second
did not choose. Reading the last does the same in reverse. There is no correct
pick, so `ErrDuplicateFileParser` refuses the request.

The parser entry also decodes with `DisallowUnknownFields`. A caller that wrote
`engines` instead of `engine` would otherwise get the native default and never
learn the option did nothing. That is the same silence this task removes one
level up, so it gets the same answer.

A first pass reads the identifier alone and tolerates unknown fields. Without
that pass, a request naming the `web` plugin with its own options draws a
decode failure about a parser field. The caller needs a refusal that names the
plugin instead.

## The cache key had to move

`inference.ChatRequest` carries `DocumentParser`, and
`internal/response/cache/identity.go` hashes that struct whole. The same bytes
read by two engines are two different inputs to the model, so the key has to
separate them.

`SemanticKeyVersion` goes from 4 to 5 in the same change, which is what the
constant's own comment requires. Entries a running gateway holds keep the `v4`
prefix and are never read back under an encoding that did not write them.

Two guards caught this before it shipped, and both were already in the tree.
The test `TestEveryRequestFieldIsKeyedOrExempt` walks `ChatRequest` by
reflection. It failed until `DocumentParser` reached the keyed table. The test
`TestTextOnlyKeyIsPinnedToItsVersion` failed until the pinned digest moved.
The test `TestChangingAKeyedFieldChangesTheKey` then proves the field reaches
the key rather than merely appearing in a list.

## Two existing tests changed meaning

`TestGatewayPluginsAreReportedNotForwarded` and
`TestUnenforcedProviderFieldsHeader` both drove the `web` plugin through a
successful request. Both now drive `file-parser`, because `web` is a refusal.

Both still assert that `plugins` appears in
`X-Starport-Unenforced-Provider-Fields`. That report is stale for the one
plugin this gateway runs. It stays until PLG7, which owns ending it, and the
assertion is that task's fail-before evidence. The controller test also gained
the refusal case, which asserts HTTP 400 and that the body names `file-parser`.

## One condition in the gate changed scope

`PLG-V02` asserted that no Go file under `internal/` spells `mistral-ocr`. The
refusal test has to name the vendor engine to refuse it, so the check now
excludes `*_test.go`. The check still holds the same property. No accepted
value in production source names an engine this gateway cannot route to.

## Evidence

```
bash scripts/verify-document-parser.sh          Summary: 3 passed, 17 failed
go test ./internal/protocol/...                 ok
bash scripts/verify-v1-architecture.sh          Summary: 12 passed, 0 failed
go test ./...                                   exit 0
go vet ./...                                    clean
make lint                                       0 issues
bash scripts/verify-openrouter-parity.sh        Summary: 16 passed, 0 failed
bash scripts/verify-dependency-direction.sh     Summary: 6 passed, 0 failed
bash scripts/verify-package-layout.sh           passed
bash scripts/verify-model-modalities.sh         Summary: 26 passed, 0 failed
bash scripts/verify-files-api.sh                Summary: 22 passed, 0 failed
bash scripts/verify-async-media-jobs.sh         Summary: 18 passed, 0 failed
bash scripts/verify-catalog-performance.sh      Summary: 20 passed, 0 failed
bash scripts/verify-auth-onboarding.sh          Summary: 26 passed, 0 failed
bash scripts/verify-console-session-grants.sh   Summary: 16 passed, 0 failed
bash scripts/verify-v1-release.sh               Summary: 16 passed, 0 failed
bash scripts/verify-developer-experience.sh     Summary: 47 passed, 0 failed
bash scripts/verify-catalog-driven-providers.sh Summary: 19 passed, 0 failed
bash scripts/verify-doc-links.sh                PASS documentation links
```

New tests: six in `internal/protocol/openrouter/plugins_test.go`, one keyed
field case in `internal/response/cache/keyfields_test.go`, and one refusal case
in `internal/server/controllers/chat_test.go`.

This task did not run `bash scripts/benchmark-overhead.sh` or `bash
scripts/smoke-openrouter-sdks.sh`. Mark both UNVERIFIED. PLG7 runs the SDK
smoke check, because that is where the parity surface changes.
