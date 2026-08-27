# AMJ1 The job seam

AMJ1 creates `internal/jobs`. It adds no storage, no route, and no provider
call.

The package owns four things:

- the record of work that outlives its request,
- the five state words AMJ0 read,
- one transition table,
- the repository contract AMJ2 implements.

## What the record holds

| Field | Why it is there |
| --- | --- |
| `ID` | the Starport identifier a caller comes back with |
| `Tenant` | the owner, so a store cannot answer with a record its caller does not own |
| `Model` | what produced the work, which AMJ7 prices |
| `Operation` | a `routing.Operation`, so the operation vocabulary stays in one place |
| `State` | where the job got to |
| `CreatedAt` | when the provider accepted it |
| `TerminalAt` | when it ended, which starts the retention window |
| `providerJobID` | unexported, and the reason this seam exists |

The package imports `internal/routing` and nothing else inside the repository.
That package is a leaf with no internal imports of its own. The job seam
therefore gains the operation vocabulary and no transitive weight.

## The five states, and the table

| From | To |
| --- | --- |
| `queued` | `running`, `completed`, `failed`, `cancelled` |
| `running` | `completed`, `failed`, `cancelled` |
| `completed` | none |
| `failed` | none |
| `cancelled` | none |

Three rules in the table need a reason.

First, a queued job reaches the completed state directly. A provider may report
a finished job on the first poll and never report a running one. A table that
demanded the running state first would refuse a real answer.

Second, no state reaches itself. A repeated poll that answers the same word
reports no change. A table that accepted the pair would restamp the terminal
time on every poll. Both the retention window and the accounting rule read that
time.

Third, every state this package knows appears as a key, and each terminal state
maps to an empty set. A missing key therefore means an unknown word rather than
a terminal state. A provider word that nothing mapped transitions nowhere in
either direction, instead of behaving like a finished job.

One method changes a state. `Transition` stamps the end time on a terminal
move. A caller therefore cannot reach a terminal state without recording when
it arrived. Two records would result from trying, and `Validate` refuses both.
One is a terminal state with no end time. The other is a live state that
carries one.

## Keeping the provider identifier in

The first invariant of the plan is that a caller never learns the provider job
identifier. A caller that learned it could poll the provider directly. That
poll would sit outside every limit Starport applies and every usage record it
keeps.

Three things hold the invariant. The third was a finding rather than a plan.

1. Nothing exports the field. No encoder, no template, and no response body
   reaches it.
2. `AdoptProviderJob` writes the value and `HasProviderJob` reports its
   presence. Nothing reads it back out. This package polls for the caller, so
   nothing outside it needs the value.
3. `Job` carries a `String` method. Go prints unexported fields under `%v`. A
   record with no `String` method would put the identifier in the first log
   line that took a whole job.

The file seam holds a blob key the same way and has the same `%v` exposure.
That record never reaches a log line as a whole value today, so this task
records the fact and changes nothing there. A repair belongs to a task that
owns the file seam.

## The tests, and where they sit

The test file declares `package jobs_test`. A test that can see the unexported
field cannot prove that a caller cannot. The file therefore sits outside the
package and uses only what a caller uses. The architecture test needs no
change, because `TestApprovedInternalPackageLayout` accepts the `_test`
suffix.

| Test | What it holds |
| --- | --- |
| `TestTheTransitionTableAcceptsOnlyTheLegalPairs` | all twenty-five pairs against an expectation written out rather than derived |
| `TestAStateNeverTransitionsToItself` | a repeated poll reports no change |
| `TestAnUnknownStateWordTransitionsNowhere` | an unmapped provider word enters the graph in neither direction |
| `TestNoTerminalStateAcceptsATransition` | a refused move changes neither the state nor the end time |
| `TestATerminalMoveStampsItsEndOnce` | state and end time stay paired through a whole run |
| `TestValidateRefusesARecordAStoreCannotAnswerWith` | eight records a store must not hold |
| `TestTheRecordHandsOutNoProviderJobIdentifier` | no exported field, no exported method, no `%v`, no `%+v`, and no JSON body carries the value |
| `TestAdoptProviderJobRefusesAnEmptyAnswer` | a provider that named no job leaves the record unadopted |

The expectation in the first test repeats the shape of the source table on
purpose. A table edit that widens the graph has to land twice before it ships.

## The repository contract

`Repository` declares `Create`, `Get`, `List`, `Replace`, and `Delete`. Every
method takes the tenant. A store therefore cannot answer with a record its
caller does not own.

`Replace` carries the whole record rather than a state word. A state change is
never the only change: a terminal move stamps a time, and a provider answer
records an identifier with it.

The errors land with the contract rather than with its implementation.
`ErrJobNotFound` answers for a job another tenant owns. A refusal would confirm
that the identifier exists, and an identifier is the only thing a caller needs
to guess. AMJ2 implements the contract over `internal/storage`.

## Fail-before

Before this task, `internal/jobs` did not exist. Both `AMJ-V01` and `AMJ-V02`
failed, and the gate reported `Summary: 0 passed, 18 failed`.

## Acceptance

| Claim | Result |
| --- | --- |
| the gate moves by exactly two conditions | `Summary: 2 passed, 16 failed` |
| `AMJ-V01` and `AMJ-V02` pass | both report `PASS` |
| the package tests pass | `ok github.com/agentstation/starport/internal/jobs`, 16 tests |
| the package layout gate passes | `package-layout verification passed` |

## Verification

- `go test ./internal/jobs/...` passes with 16 tests.
- `bash scripts/verify-package-layout.sh` passes.
- `bash scripts/verify-async-media-jobs.sh` reports `Summary: 2 passed, 16 failed`.
- `go vet ./internal/jobs/...` passes.
- `make lint` reports 0 issues.
