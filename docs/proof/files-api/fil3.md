# FIL3 the file record

Bytes alone answer no caller question. A caller asks for a filename, a size, a
purpose, a creation time, and an expiry. This task adds three things. The record
holds those answers. The repository stores it. The order of writes keeps the
record and the bytes in step.

## Two writes, one order

A file is two writes to two stores. A process can stop between them, and the
order decides what the stop leaves behind.

The record goes first, then the bytes, then the commit.

| Stop point | What is left | Who finishes it |
| --- | --- | --- |
| After the record | A pending record that names its bytes | The sweep |
| After the bytes | A pending record that names its bytes | The sweep |
| After the commit | A readable file | Nobody |

The opposite order looks simpler and is not recoverable. Bytes written before
the record would sit under a key that nothing names, and `blob.Store` lists no
keys, so no sweep could ever find them. FIL-D2 recorded that decision, and this
task implements it.

A pending record is not readable. `Get`, `Open`, and `List` all skip it, so a
caller never sees a file whose bytes are still landing.

## The grace window

The sweep deletes a pending record only after it ages past a grace window,
`DefaultPendingGrace`, one hour.

A live upload looks exactly like an abandoned one. Both cases leave a pending
record, and only time separates them. A sweep
without a window would delete the bytes under a live request, and the caller
would get a failure that no log explains.

The sweep deletes the bytes first and the record second. An interrupted sweep
then leaves a pending record, which the next sweep handles, rather than an
object no record names.

## Tenant isolation is structural

The storage key puts the tenant above the identifier:

```
files:v1:tenant:<tenant>:id:<identifier>
```

A read for the wrong tenant misses. It does not pass a check that a later
change could forget to make. The test asserts that two tenants can hold the
same identifier and each reads its own file.

A file another tenant owns answers `ErrFileNotFound` rather than a refusal. A
refusal would confirm that the identifier exists, and an identifier is the only
thing a caller has to guess.

## The blob key stays inside

`File` exports every field a caller may read. The blob key is not one of them.
It sits in an unexported field, and only the durable `fileRecord` carries it
into the record store.

The key is the one value that turns knowledge of a file into access to its
bytes across every tenant boundary. An exported field would put it in a
response body the moment a codec marshalled the record, and nothing would
report the leak.

The test walks the exported fields with reflection, so it covers a field a
later change adds. It then marshals the whole record and asserts the key is
absent. It marshals the durable form and asserts the key is present.

Because the key never leaves, `internal/files` owns byte access itself. `Open`
returns the record and a reader. A caller in FIL4 never holds a key.

## Purposes

The gateway accepts `user_data` and `vision`. OpenAI names several more, and
each of the others belongs to a product Starport does not run: an assistant, a
batch, or a fine-tune. A gateway that accepted them would take an upload it can
never use and bill storage for it.

The refusal happens before any write, so a refused purpose leaves no record for
the sweep to find.

## The import rule

`internal/files` reaches `internal/blob` and `internal/storage`, and nothing
else. The import graph test states the rule.

A dependency on routing, execution, or a protocol codec would let the meaning
of a request decide what a stored file is. A file is a file before any request
names it.

## Acceptance

| Condition | Meaning | Proof |
| --- | --- | --- |
| FIL-V07 | Another tenant reads not-found | `TestAnotherTenantReadsNotFound` |
| FIL-V08 | A crash before the commit leaves no reachable file | `TestCrashBeforeTheCommitLeavesNoReachableFile` |
| FIL-V09 | The record exposes no blob key | `TestRecordExposesNoBlobKey` |

The FIL-V08 test writes the pending record and the bytes and then stops, which
is what a killed process leaves behind. Nothing else runs, so no cleanup path
can hide the state the sweep has to handle.

The test then proves three things in order. A sweep inside the window leaves
the bytes alone. A sweep past the window deletes the bytes and then the
record. A second sweep is safe.

`TestSweepLeavesACommittedFileAlone` guards the other direction. A sweep that
read the age and not the state would delete every file the moment it aged past
the window.

## Fail-before

No file record type existed. `scripts/verify-files-api.sh` reported
`6 passed, 16 failed`.

## Verification

```bash
go test ./internal/files/... ./internal/blob/... ./internal/architecture/...
bash scripts/verify-dependency-direction.sh
bash scripts/verify-files-api.sh
```

The gate now reports `10 passed, 12 failed`. FIL-V01 through FIL-V09 pass.
