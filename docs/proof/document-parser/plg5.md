# PLG5 The extraction cache

One document reads once for each account, engine, and catalog generation inside
the window. A turn that reused every attachment says so.

## What changed

The file `internal/document/cache.go` holds the cache. It names a key, a
reading, and the narrowest store contract that expresses a window. The package
stays a leaf. It imports the standard library alone, so it names no store and
decides nothing about where a deployment keeps its data.

The file `internal/proxy/parser.go` looks the key up before it reads the
document. A hit skips both the native read and the recognition call. A miss
reads the document, and the code writes the result under the same key.

The file `internal/app/app.go` builds the cache over the key-value store under
the `extraction:` prefix, which `internal/storage/keys.go` now names. Text a
parser wrote and text a model answered never share a key.

The response carries `ExtractionCached`. It is true when every document the
turn attached came back from the cache. That answers the question a caller
asks: did this turn pay to read its attachments? A stream reports nothing,
because a stream has no place to state it.

## The key

The account, the content hash, the engine, and the catalog generation together
scope an entry.

The account keeps one tenant's read away from another tenant. The content hash
names the bytes, whatever the caller called the file. The engine matters most:
the native engine reads a scanned page as nothing, and the recognition engine
reads it as its contents. An entry that ignored the engine would hand the model
an empty document and no error.

The key hashes a JSON payload rather than joining the fields. The fields are
opaque strings a caller can influence. Joining them would let one field's value
spell another field's separator and read an entry it does not name.

## Deviation from the plan text

PLG5 step 2 says to key the cache by the hash, the engine, and the resolved
offering. This task keys it by the catalog generation instead, and records the
offering inside the entry.

Three reasons drove the change. The router resolves an offering after planning,
and the lookup must run before the paid call, so the offering is not known in
time. Invariant P4 states the rule as one read for each content hash and engine
pair, and names no offering. A key holding the offering would also miss
whenever routing shifted, which is the case the cache exists for.

The generation covers what the offering was there to cover. Any change to the
recognition offering set produces a new generation, so a changed offering
already misses.

## How it fails

An unreachable store, a corrupt record, or a missing catalog generation all
mean the same thing at the proxy: read the document. The code logs at debug
level and never fails the turn. A gateway whose document turns stop working
when its cache does is worse than one that reads a page twice.

An incomplete key is different. It neither reads nor writes, because serving
under a partial key would cross an account, an engine, or a generation.

## A time budget that held on one platform alone

The Windows job caught a defect PLG2 left. The reader checks the deadline
between pages by reading the context error, which a timer sets. A timer fires
on its platform's granularity, and Windows rounds to about fifteen
milliseconds. A document reads faster than that, so the budget went unenforced
there.

The file `internal/document/pdf.go` now compares the deadline against the clock
as well. The file `internal/document/deadline_test.go` locks the rule over a
stub context. The stub deadline sits in the past and the stub timer stays quiet. A real
timeout shows the gap on one platform alone.

## Evidence

| Command | Result |
| --- | --- |
| `go test ./...` | exit 0 |
| `go vet ./...` | clean |
| `make lint` | 0 issues |
| `make build` | `Build complete: ./starport` |
| `go test ./internal/document/... ./internal/response/cache/...` | ok, ok |
| `bash scripts/verify-document-parser.sh` | 13 passed, 7 failed |
| `bash scripts/verify-catalog-performance.sh` | 20 passed, 0 failed |
| `bash scripts/verify-v1-architecture.sh` | 12 passed, 0 failed |
| `bash scripts/verify-dependency-direction.sh` | 6 passed, 0 failed |
| `bash scripts/verify-package-layout.sh` | passed |

PLG-V12 and PLG-V13 are this task's own conditions. PLG-V14 through PLG-V20
belong to PLG6 through PLG9.

## Tests

The file `internal/document/cache_test.go` holds eleven tests over the key and
the record. Four of them carry the acceptance:

- The same bytes read once for one account, engine, and generation.
- Another account sharing those bytes gets its own entry.
- A new catalog generation misses.
- A read stops being reusable after the window.

The rest state the refusals. An incomplete key neither reads nor writes. A
corrupt record is a miss rather than text. A store failure reaches the caller
rather than a silent empty answer. A last test reads the store key and asserts
that it reveals no account, no engine, and no document.

The file `internal/proxy/parser_cache_test.go` holds five tests, and each one
counts recognition calls. A call is the unit of cost here. A cache that held
the right entries and still called the recognizer would be a cache that costs
money.

- A document a conversation resends reaches the recognizer once.
- A second account reaches the recognizer again.
- A natively read document reports the hit and reaches no provider.
- A native entry never serves a recognition request for the same page.
- A turn with no catalog generation reads its document rather than failing.
