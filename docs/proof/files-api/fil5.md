# FIL5, retention and deletion

FIL5 gives every stored file a window, and makes a delete safe to interrupt.

## Every file expires

OpenAI holds an upload until a caller deletes it. Starport does not. Storage
that only grows is an unbounded cost and an unbounded liability, so every
record carries an expiry.

| Setting | Value | Owner |
| --- | --- | --- |
| `STARPORT_FILES_RETENTION` | `720h`, which is 30 days | the operator |
| `MinRetention` | `1h` | the code |
| `expires_after[seconds]` | at most the deployment window | the caller |

An upload shortens the window. It never extends it. The service refuses a
longer window rather than clamping it. A caller that asked for a year and
silently got a month would find out when the file stopped reading.

The service also refuses a window under an hour. Such a window would expire a
file while the request that stored it is still running.

## The read decides expiry, not the sweep

The sweep runs on a ticker. A file that kept answering for the length of that
interval past its stated window would make the window a suggestion.

So `Get`, `Open`, and `List` each compare the record against the clock. An
expired file reads as not found the moment it expires. The sweep then reclaims
the storage on its own schedule. The two never have to agree on timing.

The `TestExpiredFileReadsAsNotFoundBeforeTheSweep` test moves the clock to one
second before the window and reads the file. It then moves the clock to the
window and reads not found, with no sweep between the two reads.

## A delete is two writes, and it marks first

A delete removes a record and an object in two different stores. A process can
stop between them, and the order decides what the stop leaves behind.

1. Mark the record `deleting`.
2. Delete the bytes.
3. Delete the record.

A stop after step 1 leaves a record that no caller reads and that the next
sweep finishes. The opposite order would leave a ready record over bytes that
no longer exist, and every read of it would fail with no explanation.

`TestDeleteMarksTheRecordBeforeItRemovesTheBytes` makes the byte store refuse
one key, runs the delete, and asserts the record survives marked, reads as not
found, and lists as absent. It then lets the store answer, runs the sweep, and
asserts both halves are gone. A second sweep counts nothing, because the
gateway runs it on a ticker.

## The sweep collects failures

`Sweep` handles three cases. It resumes a `deleting` record, it removes a
`pending` record older than the grace window, and it removes a `ready` record
past its expiry.

One failing record does not stop the pass. A sweep that returned on the first
error would let one unreachable object hold every later one hostage. The ticker
would then repeat the same failure forever. The pass joins its failures and
reports them together, naming each record it could not finish.

`TestSweepContinuesPastAFailingRecord` stores two files, wedges the delete of
the first, and expires both. It then asserts the healthy one went while the
failing one stayed marked and named in the error.

## A missing object is not a failure

`remove` treats `blob.ErrNotFound` as done. Both shipped backends already
answer that way, and the guard states the rule for any backend that does not.
The record is what makes a file reachable, so a second pass over a half
finished delete has to get to the record.

## Where the sweep runs

`internal/app` starts one goroutine under the runtime wait group. It runs one
pass immediately, then one per `STARPORT_FILES_SWEEP_INTERVAL`, which defaults
to an hour. The immediate pass matters: a deployment that restarts more often
than the interval would otherwise never reclaim anything.

Each pass that removed something logs its counts, so an operator can tell a
quiet deployment from a sweep that never runs. A pass that removed nothing logs
nothing.

## The wire field

A multipart form has no nesting, so the bracket is part of the field name:

```
expires_after[anchor]=created_at
expires_after[seconds]=7200
```

`created_at` is the only anchor this gateway serves. The controller refuses
another anchor, which would apply the window from a moment the caller did not
mean. Both refusals reach the caller as a 400 naming `expires_after[seconds]`.

## Acceptance

| Condition | Statement | Held by |
| --- | --- | --- |
| FIL-V14 | a delete removes both the record and the bytes | `TestDeleteMarksTheRecordBeforeItRemovesTheBytes` |
| FIL-V15 | an expired file reads as not found before the sweep runs | `TestExpiredFileReadsAsNotFoundBeforeTheSweep` |

Five more tests carry the rest of the task:

| Test | What it states |
| --- | --- |
| `TestDeleteRemovesBothWritesWhenNothingFails` | the ordinary path removes both halves |
| `TestUploadShortensTheWindowAndNeverExtendsIt` | the asymmetry, and both refusals |
| `TestEveryStoredFileStatesAWindow` | invariant F6, no record without an expiry |
| `TestSweepContinuesPastAFailingRecord` | one bad record does not hold the pass |
| `TestUploadShortensItsRetentionWindow` | the wire field, over the real routes |

## Verification

Fail-before: no record carried an expiry, and the gate reported
`14 passed, 8 failed`.

After: `bash scripts/verify-files-api.sh` reports `15 passed, 7 failed`. The
seven remaining conditions belong to FIL6 through FIL9.

`go test ./internal/files/... ./internal/blob/...` passes. `go test ./...`
passes. `make lint` reports `0 issues`.
