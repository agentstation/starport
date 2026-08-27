# FIL0 baseline

FIL0 reads the target surface and writes the gate red. It changes no production
file. The reading date is 2026-08-27.

The baseline commit is `7b224bb`, the tip of `main` after the model modalities
plan closed.

## The file object fields

The OpenAI file object carries seven live fields and two the upstream
documentation marks deprecated.

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | string | the file identifier a later request names |
| `object` | string | always the literal `file` |
| `bytes` | number | the stored size in bytes |
| `created_at` | number | the creation time in unix seconds |
| `filename` | string | the name the uploader sent |
| `purpose` | string | what the file is for |
| `expires_at` | number | the expiry time in unix seconds, optional |
| `status` | string | deprecated upstream: `uploaded`, `processed`, `error` |
| `status_details` | string | deprecated upstream, fine-tune validation only |

Source: `developers.openai.com/api/reference/resources/files`. The
`platform.openai.com` mirror answers a fetch with HTTP 403, so it is not a
readable source here.

Starport serves `status` and omits `status_details`. Two reasons hold that
split. A strict SDK decode reads `status`, and Starport holds a real two-state
record that maps onto `uploaded` and `processed`. Nothing in Starport validates
a fine-tune file, so `status_details` would carry an invented value.

## The purposes this gateway accepts

OpenAI accepts eight purposes. Starport accepts two of them.

| Purpose | Decision | Reason |
| --- | --- | --- |
| `user_data` | accept | the general purpose for a file a caller later names |
| `vision` | accept | the purpose for an image a chat request reads |
| `assistants` | refuse | Starport runs no assistants API |
| `assistants_output` | refuse | an output purpose a caller never uploads |
| `batch` | refuse | Starport runs no batch API |
| `batch_output` | refuse | an output purpose a caller never uploads |
| `fine-tune` | refuse | Starport runs no fine-tuning |
| `fine-tune-results` | refuse | an output purpose a caller never uploads |

A refused purpose returns a typed error that names the accepted set. FIL4 owns
that error.

## The routes and the envelope

Five paths make the surface. FIL4 registers them.

| Method and path | Meaning |
| --- | --- |
| `POST /v1/files` | uploads bytes and returns a file object |
| `GET /v1/files` | lists file objects for the calling tenant |
| `GET /v1/files/{file_id}` | returns one file object |
| `DELETE /v1/files/{file_id}` | deletes the record and the bytes |
| `GET /v1/files/{file_id}/content` | returns the stored bytes |

The upload accepts `file`, `purpose`, and `expires_after` with an `anchor` and
a `seconds` count. The list accepts `after`, `limit`, `order`, and `purpose`.
The list envelope holds `object`, `data`, `first_id`, `last_id`, and `has_more`.

OpenAI caps one upload at 512 MB. Starport sets its own bound instead, because
the bound belongs to the deployment rather than to the wire format. FIL6 owns
that bound.

## The object store this deployment can reach

The object store is S3-compatible, reached through
`aws-sdk-go-v2/service/s3` with a configurable endpoint. That one client
reaches AWS S3, Cloudflare R2, MinIO, and Backblaze B2.

The repository already carries the same SDK family. `go.mod` holds
`aws-sdk-go-v2` at v1.43.7, its `config` module, and `service/secretsmanager`.
A new dependency on `service/s3` therefore adds a service client, not a second
credential chain.

## Why a new seam

`internal/storage` is a byte-valued key-value store over badger and valkey. It
owns transactions, batches, and compare-and-set. Those properties suit small
records that change. They do not suit a multi-megabyte value a caller streams
once and reads whole.

The current request bound also shows the gap. `config.DefaultMaxRequestSize` is
33554432, which is 32 MiB, and the environment reads it as `MAX_REQUEST_SIZE`.
The media controller sets `maxMediaUploadBytes` to `64 << 20` and
`maxMediaMemoryBytes` to `8 << 20`. Each bound holds a whole body in memory.

`internal/blob` therefore owns opaque bytes over streams, and `internal/files`
owns the record that names them.

## Fail-before

At commit `7b224bb`:

- No directory `internal/blob` exists.
- No directory `internal/files` exists.
- No route under `/v1/files` exists. A search of `internal/server` for
  `/v1/files` and for `file_id` returns nothing.

## Acceptance

| Condition | Evidence |
| --- | --- |
| the gate is red at 22 conditions | `Summary: 0 passed, 22 failed` |
| the object fields are named | the table above |
| the accepted purposes are named | the table above |
| the object store is named | S3-compatible through `aws-sdk-go-v2/service/s3` |
| no production file changed | the branch adds `scripts/verify-files-api.sh` alone |

## Verification

- `bash scripts/verify-files-api.sh` reports `Summary: 0 passed, 22 failed` and
  exits 1.
- `go build ./...` exits 0.
