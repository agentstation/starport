# FIL6, the stored byte limit

FIL6 bounds how many bytes one account keeps, and checks the bound before the
write rather than after it.

## Stored bytes are a level, not a rate

The limit vocabulary already bounds requests, spend, and tokens. Each of those
is a rate: a fixed window opens, a holder spends inside it, and the window
resets. Stored bytes are not. Nothing resets the total at an interval boundary.
A write raises it and a delete lowers it, and the number in between is a
standing amount.

So `Limits` gains `StoredBytes *int64` rather than another `Budget`. A `Budget`
carries an interval, and an interval on a level would be a field with no
meaning.

## Two bounds, one counter

`TightestStoredBytes` resolves the account bound and the key bound to the
smaller of the two. That is the opposite of what `RequestRules` does, and the
shapes of the two meters give the reason.

| Meter | Populations | Resolution |
| --- | --- | --- |
| Request rate | two: every key the account holds, and one key | run both meters |
| Stored bytes | one: the account's files | run the smaller bound |

A stored file belongs to an account. A key that uploaded it holds no bytes of
its own, so both bounds read the same counter. Running the smaller one
satisfies the larger by arithmetic. A key bound is an operator asking that this
key stay under a tighter number than the account's own.

## The claim goes in before the check

`StorageMeter.Reserve` increments first, reads the new total, and rolls the
claim back when it does not fit. The obvious order is the wrong one. A meter
that read the total, decided, and then wrote would let two concurrent uploads
both read a total that admits them. Both would then write past the bound.

The `TestConcurrentReservationsCannotBothPassABoundThatAdmitsOne` test starts
eight goroutines on one release channel. Each reserves 600 bytes against a
bound of 1000, and exactly one lands.

The fake counter holds a mutex, which is the point. A read-then-write meter
would still pass a serial test. Only a genuinely atomic counter separates the
two implementations.

## The claim goes in before the write

Invariant F7 asks for the check before the write completes, because a check
afterward has already spent the storage it exists to protect.

The multipart parse reads the part to memory or to a spill file before the
controller reaches it. So `header.Size` states the real weight rather than a
caller's claim. `Upload` reserves that number, writes, and then settles the
claim against what the write reports.

A caller that understated the upload gains nothing. The settle raises the claim
to the real size, and the service refuses and removes an upload that no longer
fits. A caller that overstated it gets the room back rather than holding
storage it never used.

## The claim rides on the record

A crashed upload leaves nothing in memory to unwind, so the pending record
carries the claim in its `Bytes` field. The sweep that deletes an abandoned
record reads the number off it and gives it back.

Every path that drops a file gives the same number back: a failed create, a
failed write, a failed commit, a delete, and a sweep. The release runs after
both writes are gone. A failure between them therefore cannot credit a tenant
for bytes a later sweep still has to find.

A release that fails leaves the total too high. That refuses an upload the
tenant could make, which an operator sees and corrects. Too low would let a
tenant past the bound the meter exists to hold.

## The refusal says which bound it was

An upload can fail two ways that both read as too large, and they need
different answers.

| Refusal | Status | What fixes it |
| --- | --- | --- |
| `ErrUploadTooLarge` | 413 | a smaller file |
| `ErrStorageFull` | 413 | deleting a file, or a larger bound |

The second message says so: `The stored file limit for this account is full.
Delete a file to make room.` A retry of the same request fixes neither. A
caller that could not tell them apart would retry the wrong one.

## The meter counts an unbounded holder too

`Reserve` with a bound of zero still increments. An operator who sets a bound
later would otherwise read a total of zero over storage that is already full.

## Where it composes

`internal/app` builds one `limits.StorageMeter` over the durable store and
hands it to the file service.

The limit vocabulary stays a leaf. It names a `Counter` interface with the
increment and decrement it needs, and the store satisfies it structurally.

The `internal/files` package does the same. It names a `Meter` interface rather
than importing the vocabulary. A stored file knows its size and its owner, and
nothing else about the bound.

## What FIL6 leaves alone

The bound reaches a tenant and a key through the existing `limits.Limits`
field on each record. The admin plane therefore reads and writes it with no
further change. Reporting the current total on the tenant read is a separate
surface, and no condition in this plan asks for it.

## Acceptance

| Condition | Statement | Held by |
| --- | --- | --- |
| FIL-V16 | the limit vocabulary bounds stored bytes | `Limits.StoredBytes` and `TestStoredBytesJoinsTheLimitVocabulary` |
| FIL-V17 | two concurrent uploads cannot both pass a bound that admits one | `TestConcurrentReservationsCannotBothPassABoundThatAdmitsOne` |

Eleven more tests carry the rest of the task:

| Test | Package | What it states |
| --- | --- | --- |
| `TestReleaseLowersTheTotalByTheFileSize` | `limits` | the meter gives room back |
| `TestEachHolderCountsOnlyItsOwnBytes` | `limits` | one busy tenant cannot exhaust another |
| `TestAnUnboundedHolderStillCountsItsBytes` | `limits` | a bound set later reads a true total |
| `TestReserveReportsACounterFailure` | `limits` | an unreachable counter refuses the write |
| `TestTheMeterNamesItsHolder` | `limits` | the guards on holder and counter |
| `TestAnUploadPastTheBoundWritesNothing` | `files` | invariant F7, no partial blob |
| `TestADeleteLowersTheTotalByTheFileSize` | `files` | the room a delete frees is usable |
| `TestTheClaimSettlesAgainstTheRealSize` | `files` | understating an upload buys nothing |
| `TestAFailedWriteGivesBackItsClaim` | `files` | a write that never landed costs nothing |
| `TestTheSweepGivesBackAnAbandonedClaim` | `files` | the claim survives a crash on the record |
| `TestAFullAccountRefusesAnUploadAndSaysWhy` | `server` | the wire refusal, over the real routes |

## Verification

Fail-before: the limit vocabulary named no byte bound, and the gate reported
`15 passed, 7 failed`.

After: `bash scripts/verify-files-api.sh` reports `17 passed, 5 failed`. The
five remaining conditions belong to FIL7 through FIL9.

`go test ./internal/limits/... ./internal/tenant/... ./internal/files/...`
passes, and the limits package passes under `-race`. `go test ./...` and
`go vet ./...` pass. `make lint` reports `0 issues`.
`bash scripts/benchmark-overhead.sh` passes.
