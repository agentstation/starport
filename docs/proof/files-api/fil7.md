# FIL7, a stored file in a chat request

FIL7 lets a document content part name a stored file instead of carrying its
bytes. The gateway resolves the reference inside the requesting account, once
for each request.

## Why the bytes land in the URL field

A resolved document reaches the provider connector through
`inference.Document.URL` as a data URL. That looks like the wrong field until
you read `internal/providers/connectors/media.go`. The connector builds its
wire part from `Document.URL` alone, and refuses a document part whose URL is
empty. No producer anywhere sets `Document.Data`. Every decode path already
puts the whole data URL in `Document.URL`.

So the resolved part and an inline part are the same value. That makes the
first half of FIL-V18 a real test rather than a shape check. The test runs two
requests through two routers, then compares the two `connectors.ChatRequest`
values for equality.

The resolver also clears `FileID` on the part it writes. Nothing downstream
can learn which of the two spellings the caller used.

## Where resolution sits

Resolution runs inside `ProcessChatCompletion` and
`ProcessChatCompletionStream`, after validation and before
`TransformChatRequest`. Three boundaries decide that position.

| Boundary | What it owns | Why resolution is not inside it |
| --- | --- | --- |
| The cache middleware | the semantic key | the key must hold the identifier, not the bytes |
| `router.RouteWithFallback` | the attempt loop | a read for each attempt would send two documents on a retry |
| The HTTP controller | the caller's request | a controller cannot reach the byte store without a new dependency |

The middle row is the sharp one. A file can expire between two attempts. A
gateway that read inside the attempt loop would send one document to the first
provider and a different one, or nothing, to the second. One request would then
ask two questions. `TestARetryReadsTheStoredBytesOnce` drives a router that
runs the attempt hook three times and asserts one read.

## The identifier keys the cache, not the bytes

`CacheMiddleware.Wrap` computes the key before the proxy resolves anything, so
the key covers `Document.FileID` and an empty `Document.URL`. That is the
behavior FIL-V19 asks for, and it costs one lookup that never touches the byte
store.

A stored file is cache-eligible, and a remote URL still is not. The rule that
separates them is a promise about the bytes.

| Reference | Can the bytes change under a cached answer? |
| --- | --- |
| a remote URL | yes, the owner controls them |
| inline bytes | no, the request carries them |
| a stored file | no, the gateway wrote them once and never rewrites them |

The gateway writes a stored file once and then deletes it. It never rewrites
one, and it never reuses an identifier. The identifier therefore names one
fixed payload for as long as it resolves at all. That is what the inline rule
asks of a digest.

`SemanticKeyVersion` rises from 3 to 4 because the canonical request now
encodes a field it did not encode before. The version constant carries that
instruction, and `TestTextOnlyKeyIsPinnedToItsVersion` enforces it. Stale
entries keep the `v3` prefix that wrote them.

## Another account's file is a miss

`TestAnotherTenantsFileIsNotFoundBeforeAnyProviderCall` states two things at
once. The refusal lands before the router, so no credential and no caller money
pays for a file the caller may not read. And an identifier another account
holds reads exactly like an identifier nobody holds. A different answer would
tell one account which identifiers another account has.

The tenant check lives inside `files.Service.Open`, which already takes a
tenant argument. So a foreign identifier is a miss inside the owning concept
rather than a check the adapter could forget.

`ErrStoredFileNotFound` maps to 404 in `errorShape`. An unreachable byte store
is a different answer. The
`TestAnUnreachableFileStoreFailsTheRequest` test asserts that it does not read
as not found. That answer would tell a caller to stop retrying a request a
working store would answer.

## The port, not the package

`internal/proxy` names a `FileResolver` port with one method, and
`internal/app` adapts the file service to it. The proxy never imports
`internal/files`, so the file vocabulary stays inside the concept that owns it.

The port answers `(StoredDocument, bool, error)`. The found flag keeps the file
package's error sentinels out of the proxy, and the adapter translates
`files.ErrFileNotFound` into an absent result.

## A document names one source

`Document` now names three sources for the same bytes: inline `Data`, a `URL`,
and a `FileID`. A part that named two would leave the answer to whichever one a
codec read first.

`Document.Validate` states the rule once, at the concept that owns the type.
Three surfaces call it:

- each protocol codec, at its own wire field path
- `ValidateChatCompletionRequest`, for a canonical request no codec built
- the codec tests, over the real decode path

The refusal names the rule rather than the fields that collided. The two
protocol families spell these sources differently on the wire. A message that
named a canonical field could therefore name a field the caller never wrote.
The field path a codec wraps around the error is what locates the part.

Both codecs previously refused `file_id` by name. That refusal is now a decode
into `Document.FileID`, and the message for a part that names nothing changes
to `content[0].file needs file_data or file_id`.

## The inline path pays nothing

`namesStoredDocument` walks the messages first. A request that names no stored
file returns unchanged: no clone, no map, and no resolver call.
`TestAnInlineRequestNeverReachesTheResolver` holds that.

A request that does name one gets a clone. The
`TestResolutionLeavesTheCallersRequestAlone` test asserts that the caller's
request still holds the identifier after the call. A middleware above the proxy
may read it.

## One read for each file, not for each part

A request may cite the same document in several parts. The `resolveDocuments`
function carries a map of what it already read. The count of reads therefore
follows the count of distinct files.

## What FIL7 leaves alone

The record carries a filename and no media type, so the stored extension is the
only clue to what the bytes are. The `documentMediaType` function reads it
through `mime.TypeByExtension`, and falls back to `application/octet-stream`. A
part that carries its own filename keeps it. A caller that named the part meant
that name to reach the model.

`internal/app` wires the resolver only when the deployment stores files. A
gateway without file storage refuses a `file_id` with a validation error that
says so, rather than sending a model an empty document part.

## Acceptance

| Condition | Statement | Held by |
| --- | --- | --- |
| FIL-V18 | a document content part can name a stored file identifier | `Document.FileID` and the two tests below |
| FIL-V19 | a stored file reference reaches the cache key | `TestTwoStoredFilesKeyApart` |

| Test | Package | What it states |
| --- | --- | --- |
| `TestAStoredFileReachesTheProviderAsInlineBytesDo` | `proxy` | FIL-V18, the two spellings reach one request |
| `TestAnotherTenantsFileIsNotFoundBeforeAnyProviderCall` | `proxy` | FIL-V18, a foreign identifier stops before the router |
| `TestARetryReadsTheStoredBytesOnce` | `proxy` | one read for each request, not for each attempt |
| `TestOneRequestNamingAFileTwiceReadsItOnce` | `proxy` | one read for each distinct file |
| `TestResolutionLeavesTheCallersRequestAlone` | `proxy` | the resolved bytes stay inside the proxy |
| `TestAnInlineRequestNeverReachesTheResolver` | `proxy` | the inline path pays nothing |
| `TestAGatewayWithoutFileStorageRefusesAFileReference` | `proxy` | a deployment without storage says so |
| `TestAnUnreachableFileStoreFailsTheRequest` | `proxy` | a broken store is not a missing file |
| `TestARequestNamingTwoDocumentSourcesIsRefused` | `proxy` | the canonical backstop, with its field path |
| `TestTheStoredNameDecidesTheMediaType` | `proxy` | the filename is the only evidence of the type |
| `TestAStoredFileStaysCacheEligible` | `cache` | a stored reference is not a remote one |
| `TestAStoredFileAndItsBytesKeyApart` | `cache` | resolution does not collapse two entries into one |
| `TestADocumentNamesOneSourceOrNone` | `inference` | the exclusivity rule, over all three pairs |
| `TestAnEmptyFilenameIsNotASource` | `inference` | a described reference is still one source |
| `document by stored reference` | `openai`, `openrouter` | the wire word decodes into the canonical field |
| `document naming both its bytes and a stored reference` | `openai`, `openrouter` | each codec refuses at its own path |

## Verification

Fail-before: a content part could not name a stored file, and the gate reported
`17 passed, 5 failed`.

After: `bash scripts/verify-files-api.sh` reports `19 passed, 3 failed`. The
three remaining conditions belong to FIL8 and FIL9.

`go test ./internal/inference/... ./internal/protocol/... ./internal/proxy/...
./internal/response/cache/...` passes. `go test ./...` and `go vet ./...` pass.
`make lint` reports `0 issues`.
`bash scripts/verify-openrouter-parity.sh` reports `16 passed, 0 failed`.

`ValidateChatCompletionRequest` crossed the cyclomatic bound of 30 when it
gained the document rule, so its message loop moved into `validateMessages`. A
message is its own shape, and the split follows that seam rather than the
lint number.
