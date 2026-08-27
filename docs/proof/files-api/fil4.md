# FIL4 the file routes

FIL3 built a stored file that no caller can reach. This task publishes five
routes over it, guards them with two scopes, and encodes the object exactly as
FIL0 recorded it.

## Five routes, two scopes

| Route | Scope |
| --- | --- |
| `POST /v1/files` | `files:write` |
| `GET /v1/files` | `files:read` |
| `GET /v1/files/{file_id}` | `files:read` |
| `DELETE /v1/files/{file_id}` | `files:write` |
| `GET /v1/files/{file_id}/content` | `files:read` |

Reading and writing are separate powers, so they are separate scopes. An
upload consumes the storage of the deployment. A key that may send a prompt
should not thereby be able to fill a disk.

Neither scope is `chat:write`. A tenant that reads a stored document and a
tenant that stores one are different trust decisions, and an operator makes
each one separately.

The anonymous identity carries both. An operator who disabled authentication
trusts the port. Half a surface would fail the one mode that exists to make the
first request work.

## Two bounds, one exemption

The gateway already bounds a request body. That bound protects the decoder: it
caps the JSON document the gateway reads into memory. Its default is 32 MiB.

An upload is not a document the gateway decodes. It streams to the byte store.
A deployment sizes it against its storage rather than against its heap. The two
numbers answer different questions, so the upload carries its own bound at
`FilesConfig.MaxUploadBytes`, with a default of 512 MiB.

Without an exemption the smaller number would silently win. An operator who
raised the upload bound would see no change. The `SizeLimiter` middleware now
takes a predicate, and `carriesOwnBodyBound` names the upload path.

The exemption is narrow on purpose. It matches one path, and the handler
behind that path applies its own bound as the first thing it does.

## The bound runs before the store

`http.MaxBytesReader` wraps the body before the multipart parse. An upload
past the bound therefore fails during the read, and `blob.Put` never runs.

A bound checked after the read would leave the object it exists to prevent.
The test asserts the refusal and then counts the files under the byte store
root, staging files included. The count is zero.

The same test then stores a small file through the same server. A route that
refused everything would otherwise satisfy the first half.

## Why the parse and not the reader

`multipart.Reader` streams one part at a time and would let the gateway avoid
a temporary file. It also makes field order load-bearing: the `purpose` field
would have to arrive before the `file` field, and no client promises that.

`ParseMultipartForm` keeps 8 MiB in memory and spills the rest to disk. A
large upload costs disk rather than heap. A deferred `RemoveAll` drops the
temporary files, and the field order no longer matters.

## The codec holds no stored file

`internal/architecture` bounds what a protocol package imports. A codec reaches
`internal/inference` and `internal/protocol/mediaform`, and nothing else. It
cannot import `internal/files`.

That rule is the reason `internal/protocol/openai` owns a standalone
`StoredFile` wire struct and the controller maps one record onto it. The wire
format states what a caller reads. It does not get to state what a stored file
is.

The names carry a `StoredFile` prefix because `File` already names the document
content part that FIL7 will teach to reference an upload.

## The status field

FIL0 recorded that Starport serves `status` and omits `status_details`.

A strict SDK decode reads `status`, and Starport holds a real two-state record
behind it. A record whose bytes have not committed reads as `uploaded`, and a
readable one reads as `processed`.

Nothing in Starport validates a fine-tune file, so `status_details` would carry
an invented value. An absent field is the honest answer.

## The download names the file safely

The uploader chose the filename, so it reaches the `Content-Disposition` header
through a filter. The plain `filename` parameter carries the printable ASCII
part. The RFC 5987
parameter carries the whole name for a current client.

A quote, a backslash, or a control character in a stored name would otherwise
let an uploader compose a header.

## What this task leaves to its successors

The upload accepts `file` and `purpose`. It does not read `expires_after`.
FIL5 owns retention, and a field decoded here without a sweep behind it would
promise an expiry that nothing applies.

The list accepts `limit`, `purpose`, `order`, and `after`, and it pages inside
the controller over one repository read. FIL6 owns the stored byte bound that
makes a large listing worth a repository-level cursor.

## Acceptance

| Condition | Meaning | Proof |
| --- | --- | --- |
| FIL-V10 | The router publishes the five paths | `TestServerRegistersTheFilePaths` |
| FIL-V11 | A read-only key cannot upload | `TestFileRoutesCarryTheirScopes` |
| FIL-V12 | An upload past the bound writes nothing | `TestUploadPastTheBoundWritesNothing` |
| FIL-V13 | The object carries every recorded field | `TestStoredFileEncodesEveryRecordedField` |

The FIL-V10 test walks the router the server built rather than reading the
source that builds it. A route mounted under the wrong group reads as present
to a source scan and answers 404 to a caller.

The FIL-V11 test covers both directions. A read-only key cannot upload. A
chat-only key reaches neither side. A read-only key still reads what a writing
key stored. A scope no key satisfies
refuses every caller as completely as a missing route.

The FIL-V13 test decodes the encoded object into a map and asserts the whole
key set. It then asserts that `status_details` is absent, so a later change
that adds the field fails here.

Four more tests hold behavior that no condition names.

| Test | What it holds |
| --- | --- |
| `TestFileLifecycleOverTheRoutes` | One file through all five routes, each step naming the identifier the last one returned |
| `TestOneTenantCannotReadAnotherTenantsFile` | The isolation rule at the HTTP edge |
| `TestUploadRefusesAPurposeThisGatewayDoesNotServe` | A refused purpose names the accepted set |
| `TestFileUploadIsNotBoundByTheJSONRequestLimit` | The exemption, against a future middleware change |

## Fail-before

Every route answered 404. `scripts/verify-files-api.sh` reported
`10 passed, 12 failed`.

## Verification

```bash
go test ./internal/server/... ./internal/protocol/... ./internal/identity/...
bash scripts/verify-auth-onboarding.sh
bash scripts/verify-files-api.sh
```

The auth gate reports `26 passed, 0 failed`. The files gate now reports
`14 passed, 8 failed`. FIL-V01 through FIL-V13 pass, and FIL-V15 passes on the
record work FIL3 landed.
