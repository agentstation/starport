# FIL1 the blob seam

FIL1 creates `internal/blob`. The package stores opaque bytes at an opaque key,
and it holds nothing else. It turns conditions FIL-V01 through FIL-V03 green.

## Why a second storage package

`internal/storage` is a byte-valued key store with transactions, batches, and
compare-and-set. Those operations suit a small record that changes. A file of
several megabytes would move through every one of them, so the bytes get their
own seam.

The split is a difference in kind, not in size alone. A caller reads a record,
changes it, and writes it back under one transaction. A file arrives once as a
stream, and a later reader wants it whole. One contract cannot serve both without weakening
the guarantees of each.

## The contract

`Store` holds four operations plus a name.

| Operation | Meaning |
| --- | --- |
| `Put` | reads a stream to its end and stores it at the key |
| `Get` | opens the stored object for reading |
| `Stat` | reports the object without reading its bytes |
| `Delete` | removes the object, and returns nil for an absent key |
| `Backend` | names the implementation for the startup report |

The contract defines two errors. `ErrInvalidKey` reports a key the rules refuse.
`ErrNotFound` reports that no object exists at the key. A backend returns these
rather than an error of its own, so a caller branches on the reason without
naming a backend.

FIL2 must add the object store without an interface change. The startup report
it owes needs `Backend`, so the contract holds that method now.

## The key rules

A key carries no structure a store reads. `ValidateKey` accepts letters,
digits, `-`, `_`, and `.`, up to 256 bytes. It refuses an empty key, `.`, `..`,
and any key holding `/` or `\`.

The path separator is the case that matters most. A filesystem reads it as a
directory step, and an object store reads it as a prefix. One key would then
name two different places on two backends. Every operation runs the same check,
so a caller cannot reach an object through one method that another refuses to
name.

## The filesystem backend

`NewFilesystem` roots the store at a configured directory and creates two
subdirectories under it: `objects` and `staging`. They are siblings on one
filesystem, so the final rename stays atomic.

A put stages the bytes in `staging`, flushes them to the medium, and renames the
staged file into `objects`. A put that fails removes its staged file. The order
gives the property FIL3 depends on. A failed put leaves no readable object at a
key that held none. It leaves the prior object intact at a key that did.

The backend names the object file by the hash of the key rather than by the key
itself. A key this package accepts is not a name every filesystem accepts. The
hash removes that whole class of difference between one host and the next. The
hash also shards the tree over two levels, so no directory grows one entry per
stored file.

A put reads through a context-aware reader. A large upload therefore stops when
the request that asked for it goes away.

## The import rule

`TestImportGraphArchitecture` now lists the package and asserts
`assertOnlyInternalImports` with no allowed internal import at all. The package
is a leaf.

A store that could reach a Starport concept would start reading meaning into the
bytes it holds. The owner of the key holds every meaning instead.

## Acceptance

| Condition | Evidence |
| --- | --- |
| FIL-V01 | `Store` names put, get, stat, and delete over streams |
| FIL-V02 | two tests refuse a key holding a path separator |
| FIL-V03 | the import graph test names the package and bounds it to no internal import |

The contract test runs one table over every registered backend. FIL2 joins the
object store to that table without a new case. The cases are:

- a put, stat, get, and delete round trip,
- a zero-byte object, which is a real object rather than an absent one,
- a missing key for get, stat, and a repeated delete,
- an overwrite, where the reported size follows the new value,
- an interrupted put at a fresh key, which leaves nothing readable,
- an interrupted put at an occupied key, which keeps the prior object,
- a canceled context,
- an invalid key refused by all four operations,
- a backend that names itself.

Two more tests hold the timing rather than the error. One walks the whole root
before and after a refused key and requires no change. It also checks the parent
directory for a name a traversal would create. The other requires the staging
directory to drain after a failed put and a successful one.

## Fail-before

At the FIL0 baseline the package did not exist. The gate reported `Summary: 0
passed, 22 failed`.

## Verification

- `go test ./internal/blob/... ./internal/architecture/...` passes.
- `bash scripts/verify-package-layout.sh` passes.
- `bash scripts/verify-files-api.sh` reports `Summary: 3 passed, 19 failed`.
- `go vet ./...` is clean, and `make lint` reports 0 issues.
