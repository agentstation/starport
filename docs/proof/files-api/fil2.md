# FIL2 the object store

FIL1 gave the byte store a contract and one backend. A filesystem directory
serves one node. Put two Starport processes behind a load balancer, and one
node answers a file while the other answers not-found. This task adds the second backend and selects between them
from configuration.

## One contract, two backends

The object store satisfies the FIL1 `blob.Store` interface with no change to
it. The contract test proves this: the backend joins a table, and every case
runs against both entries.

```go
func backends(t *testing.T) []backend {
	return []backend{
		{name: "filesystem", open: ...},
		{name: "objectstore", open: ...},
	}
}
```

The nine contract cases from FIL1 run unchanged. FIL2 added no case to the
contract, which is the property the task asked for.

## What the backend talks to

The client is `aws-sdk-go-v2/service/s3`. One client reaches AWS S3,
Cloudflare R2, MinIO, and Backblaze B2. Each of them serves the same API. The
`Endpoint` setting selects the implementation, and an absent endpoint selects
AWS itself.

An endpoint also turns on path-style addressing. An implementation other than
AWS usually serves a bucket at a path rather than at a subdomain. A bucket
name inside a hostname needs DNS that a private deployment rarely has.

`Put` takes a reader of unknown length, so it goes through the SDK uploader
rather than a single `PutObject` call. The uploader buffers the stream into
parts and signs each one. A direct call would need the length before the first
byte, which an upload that streams from a request body cannot give.

The uploader carries a deprecation notice. Its replacement,
`feature/s3/transfermanager`, is a pre-1.0 module. This package holds durable
bytes, so it waits for a stable API rather than tracking one that may still
change.

The `Prefix` setting scopes every key. Two deployments that shared a bucket
without a prefix would read each other's files, and every operation would
still report success.

## The test server

The contract runs against an in-process HTTP server that speaks the part of
the S3 API the backend calls.

A hand-written fake of the `blob.Store` interface would prove nothing about
the adapter, and the adapter is the only thing this task adds. The server
makes the real client do real work: the SDK signs the request, encodes the
body, reads the response, and maps the error.

Three server behaviors carry the contract properties:

| Behavior | Property it proves |
| --- | --- |
| A short body answers 400 `IncompleteBody` | An interrupted put stores nothing |
| A missing key answers 404 `NoSuchKey` | `Get` reports `ErrNotFound` |
| A HEAD miss answers a bare 404 with no body | `Stat` reports `ErrNotFound` without an error code |

The test payloads stay under the uploader's five megabyte part size, so the
server serves single-request `PutObject` alone. A multipart upload takes a
different path through the SDK, and no test here covers it.

## Startup reaches nothing

`NewObjectStore` builds a client and returns. It makes no network call. A gateway that reached its bucket during composition would fail to boot
whenever the store was slow or unreachable. A gateway that cannot boot cannot
serve the routes that need no files at all.

The one check the constructor makes is the bucket name. Every other
reachability question waits for the first upload.

## Selection and refusal

`FilesConfig` names the backend, and an absent value selects the filesystem.
The filesystem needs nothing configured, so a deployment that says nothing
about file storage still starts and still serves an upload.

An incomplete object store selection stops the gateway. It does not fall back
to the filesystem. A fallback is the dangerous answer. The deployment asked for a
shared store, got a per-node directory, and looks healthy until the second
node answers not-found.

`Validate` names the missing setting:

```
config: the object store configuration is incomplete: it names no bucket
config: the object store configuration is incomplete: it states one half of a
static credential pair
```

## Credentials stay out of the text

An operator pastes a startup failure into an issue, and a key inside it
becomes a key on the internet. Three rules hold the line, and a test holds
each one.

- The two key fields carry `secret:"true"`. The redacted inspection that the
  console and the CLI print omits them.
- No validation error names a credential. It names the setting the
  configuration left out.
- No operation error names a credential. One test runs a put, a get, and a
  stat against a refused bucket, then asserts on each message.

The startup report follows the same rule. It prints the backend, and it prints
the path for the filesystem or the bucket and the prefix for the object store.
The bucket is not a secret, and an operator needs it to tell one deployment
from another.

## Acceptance

| Condition | Meaning | Proof |
| --- | --- | --- |
| FIL-V04 | One contract, two backends | `internal/blob/contract_test.go` |
| FIL-V05 | An absent configuration selects the filesystem | `TestAbsentFilesConfigSelectsTheFilesystem` |
| FIL-V06 | An incomplete object store refuses startup | `TestIncompleteObjectStoreConfigRefusesStartup` |

Two composition tests hold the wiring. `TestCompositionOpensTheConfiguredByteStore`
proves the selection reaches the running application.
`TestObjectStoreCompositionReachesNoBucket` boots the gateway against an
endpoint that answers nothing.

## Fail-before

Before this task the package held one backend. The contract table had one
entry, `internal/config` had no file section, and `internal/app` opened no byte
store. `scripts/verify-files-api.sh` reported `3 passed, 19 failed`.

## Verification

```bash
go test ./internal/blob/... ./internal/config/... ./internal/app/...
bash scripts/verify-v1-architecture.sh
bash scripts/verify-files-api.sh
```

The gate now reports `6 passed, 16 failed`. FIL-V01 through FIL-V06 pass, and
the sixteen conditions FIL3 through FIL9 own stay red.
