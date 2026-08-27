# AMJ4 The provider job interface

## Outcome

A transport can now submit one provider job, poll it, and cancel it. The
registry refuses a descriptor that claims the video operation and implements
nothing. A provider state word maps onto the canonical state set, and a word no
provider vocabulary records stops the job. A poll policy bounds the request
count and the total lifetime of one job.

The gate moves from 6 of 18 to 8 of 18. `AMJ-V07` and `AMJ-V08` pass, which
closes phase B.

## The interface sits beside Connector, not on it

Invariant J5 keeps `Connector` at five methods. Three job methods on it would force a stub into every chat-only transport. A stub that answers "unsupported" reads to the compiler the same as a real implementation.

`JobRunner` in `internal/providers/connectors/job_transport.go` holds the three
methods instead. `JobRunnerFor` probes a selected transport for it. The registry
probes at activation, so a descriptor that claims `videos-generations` with no
implementation fails once at startup rather than once per request.

`mediaInterfaces` gained a `missing error` column for that reason. A media call
that fails inside its request raises `ErrTransportInterfaceMissing`. A job the transport can never start raises `ErrJobsUnsupported`. The two answers differ for a
caller, so the rows name different errors.

## The state map holds only recorded words

`providerStateWords` names the seven words AMJ0 read from the two published
video surfaces. Nothing else appears there.

| Provider word | Canonical state | Source |
| --- | --- | --- |
| `queued` | `queued` | OpenAI |
| `pending` | `queued` | OpenRouter |
| `in_progress` | `running` | both |
| `completed` | `completed` | both |
| `failed` | `failed` | both |
| `expired` | `failed` | OpenRouter |
| `cancelled` | `cancelled` | OpenRouter |

DeepInfra publishes no status vocabulary, and this machine holds no DeepInfra
credential to read one from. A guessed word would classify a real provider
answer with no evidence behind it. `ProviderJobState` therefore raises
`ErrUnknownProviderState` for anything the table omits.

That refusal is the point of the table. A word that fell through to `running`
would poll a finished job forever. A word that fell through to `failed` would
discard an asset the caller already paid for.

`TestAnUnknownProviderStateWordFailsLoudly` cross-checks the table against
`ProviderStateWords()` and asserts that `processing`, `succeeded`, `running`,
`done`, and the empty word all raise the error with no state.

## A failed job states why

AMJ5 returns the reason to a caller who polls a failed job. The reason is therefore a record rule, not a field a writer may leave empty. The record's `Validate` refuses a failed job with no reason, and refuses a reason on any other state.

`Transition` refuses `JobStateFailed` outright. A door that takes no reason
would let a record reach storage without one. The `Fail` method is the single door into that state, and it takes the reason as an argument.

Three paths reach it: a provider rejection, a provider word that names a
failure, and a job that outlived its polling budget.

`TestAFailedJobAlwaysStatesAReason` walks five provider answer shapes. Each one
builds a real `jobs.Job`, calls `Fail` with the decoded reason, and calls
`Validate`. The test proves the connector's decoder against the record's rule rather than checking each one alone.

## Two bounds on what a job costs

Polling spends the tenant's own credential on asking rather than on work, so
`PollPolicy` carries two separate bounds.

`Backoff` doubles the wait from two seconds to a thirty second ceiling. The
request count then grows with the logarithm of the wait rather than with the
wait.

The `Lifetime` bound stops the polling after an hour. Starport measures it from the moment the job started, not from the last poll. A provider that keeps reporting progress therefore cannot extend it. The `FailSpent` call moves a spent job to its terminal failed state. It states the window in the reason.

The `Spent` check reports false for a terminal job. A completed job that outlived the window still produced its asset. A sweep that failed it would discard work the tenant paid for. `TestATerminalJobIsNeverSpent` asserts this for all three
terminal states.

`FailSpent` raises `ErrJobLifetimeExceeded` for a job still inside its window.
A caller that asked about such a job would otherwise read an unchanged record as
a job it had just ended.

## One correctness fix outside the task

`send` in `openai_media.go` accepted HTTP 200 alone. An OpenRouter submission
answers 202 and a delete answers 204, so the job path would read two provider successes as failures. The check now accepts any 2xx, which is also
correct for the image, speech, and transcription paths that already use it.

## Commands

```
go test ./internal/providers/... ./internal/jobs/...   ok, 259 tests
go test ./...                                          ok, exit 0
go vet ./...                                           clean
make lint                                              0 issues
bash scripts/verify-async-media-jobs.sh                Summary: 8 passed, 10 failed
```

The `internal/jobs` package runs 31 tests and
`internal/providers/connectors` runs 228.

Nine standing gates ran clean beside them.

| Gate | Result |
| --- | --- |
| v1 architecture | 12 passed |
| dependency direction | 6 passed |
| package layout | passed |
| catalog driven providers | 19 passed |
| OpenRouter parity | 16 passed |
| model modalities | 26 passed |
| files API | 22 passed |
| auth onboarding | 26 passed |
| console session grants | 16 passed |

## Gate movement

```
PASS AMJ-V07 a descriptor claiming the operation with no interface fails activation
PASS AMJ-V08 an unknown provider state word and a spent lifetime both fail loudly
```

The ten failures that remain belong to AMJ5 through AMJ9.
