# AMJ2 Job persistence

AMJ2 implements the repository contract AMJ1 declared. A job outlives the
request that created it, and often the process too. A restart must not lose a
job the caller already holds an identifier for.

## The key puts the tenant first

A record lives at `jobs:v1:tenant:<tenant>:id:<id>`, with both parts base64
encoded so a tenant name carrying a colon cannot reach another prefix. A read
for the wrong tenant misses by construction rather than by a check a later
change could forget.

Four operations inherit the isolation from the key alone:

| Operation | Answer for a job another tenant owns |
| --- | --- |
| `Get` | `ErrJobNotFound` |
| `List` | an empty listing |
| `Delete` | success, and the owner still reads its job |
| `Replace` | `ErrJobNotFound` |

`ErrJobNotFound` answers rather than a refusal. A refusal would confirm that
the identifier exists, and an identifier is the only thing a caller needs to
guess.

## Newest first, and stable

`List` sorts after decoding rather than trusting the scan. Storage answers in
key order, and a key carries the identifier rather than the time. A caller
polling a job it just submitted looks at the top of the page. A listing ordered
by key would put that job wherever its identifier happened to sort.

The test writes three jobs whose identifiers sort against their submission
order on purpose, so a listing that leaned on the key would answer backwards.
It then reads the same store through a second repository, which is what a
restart looks like.

A tie on the submission time breaks on the identifier. Two jobs submitted
inside the same clock tick would otherwise swap places between two reads of
the same unchanged data.

## The transition table sits in front of storage

`Replace` reads the stored record, decodes it, and compares its state with the
one the caller wrote. A change that the table refuses fails here.

A store that accepted the write would hold a record no state machine produced,
and every later read would trust it. Failing at the repository also keeps the
table in one package: no store has to know which state changes this seam
allows.

`Replace` accepts a write that keeps the state. A poll that answers the same
word twice is not a transition. Refusing it would stop a provider job
identifier from ever reaching the record.

The test asserts more than the error. After two refused writes it reads the
record back, finds the state and the end time unchanged, and counts the keys
under the job prefix. The refused record never reached the store, so no sweep
has to undo it.

## The durable form carries what the record hides

`jobRecord` carries the provider job identifier that `Job` keeps unexported.
The record store is the one place the value belongs. The round trip proves the
value survives. A job adopted before a write reports `HasProviderJob` after a
read, and no test reads the value itself.

A decode that cannot produce a record reports `ErrCorruptRecord` rather than a
half-filled job. The test writes three broken values at the record key. One is
not JSON. One carries an unsupported schema version. One decodes and then fails
validation. All three report the same error.

## The dependency rule

`TestImportGraphArchitecture` lists each governed package by hand, so a new
package carries no rule until a task adds it. This task adds `internal/jobs`
to both lists and gives it an allowlist of two imports: the operation
vocabulary and the record store.

A dependency on execution or a provider connector would put the poll loop
inside the record, and the seam exists to keep the two apart. AMJ6 stores an
asset and widens the allowlist by one entry at that point.

## Fail-before

Before this task, no repository existed, so the tenant isolation test could not
compile. `AMJ-V03` and `AMJ-V04` both failed, and the gate reported
`Summary: 2 passed, 16 failed`.

## Acceptance

| Claim | Result |
| --- | --- |
| the gate moves by exactly two conditions | `Summary: 4 passed, 14 failed` |
| `AMJ-V03` and `AMJ-V04` pass | both report `PASS` |
| the package tests pass | 27 tests under `internal/jobs` |
| the import graph test names the package | `TestImportGraphArchitecture` passes |

## Verification

- `go test ./internal/jobs/... ./internal/storage/... ./internal/architecture/...` passes.
- `bash scripts/verify-async-media-jobs.sh` reports `Summary: 4 passed, 14 failed`.
- `go vet ./internal/jobs/...` passes.
- `make lint` reports 0 issues.
