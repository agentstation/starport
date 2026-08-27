# AMJ7 Accounting and outstanding job limits

## Outcome

A finished job now draws exactly one usage record. It draws one whatever a
caller does with the job afterwards. A failed job and a cancelled job draw a
record with no cost. An account holds a bounded number of jobs open. A
submission past that bound reads 429, and it reads it before the gateway
resolves a credential.

The gate moves from 13 of 18 to 16 of 18. `AMJ-V14`, `AMJ-V15`, and `AMJ-V16`
pass, which closes phase C.

## A poll is free, so the record carries the stamp

A caller polls until it sees a terminal answer. A client that retries polls
well past that, and so does a browser tab somebody left open. Every one of
those reads is a read of the same finished work. A per-read charge would bill a
tenant for asking how much it had already paid.

So the job record holds `AccountedAt`. The `settle` method stamps it before it
reports anything. The stamp is a compare and swap against the record store.
Of two concurrent polls, only one gets past it. `MarkAccounted` refuses a second
stamp rather than overwrites the first. That refusal is what makes the loser of
the race draw nothing, rather than a second cost for one video.

`TestACompletedJobDrawsOneRecordHoweverOftenACallerPolls` polls a finished job
ten times and asserts one entry. The fake keeps every entry rather than a
count. A count would pass a service that reported the same job twice with
different words.

The order gives something up. A report lost between the stamp and the recipient
is lost for good. That is the correct half to give up. The usage seam is
best-effort by construction and drops records under load already. A duplicated
charge is money a tenant did not spend.

## The state decides the cost, and the seam decides the price

`internal/jobs` owns when a job ends. It owns whether the end produced work. It
owns no price, no catalog, and no currency. `AccountingEntry` therefore carries
a state and a `Chargeable` flag. `JobState.Chargeable` reports true for exactly
one state.

The half that reads a Starmap offering is `proxy.JobAccountant`. It lives in
`internal/proxy` because that package already holds the catalog-to-cost rules
every other operation uses. A video priced by a second copy of those rules
would drift from the rest of the bill. It is deliberately not a method on
`Proxy`, because nothing on the request path calls it.

The import graph forces the split. `internal/architecture` bounds
`internal/jobs` to `blob`, `routing`, and `storage`. The job seam therefore
declares the `Accountant` and `Meter` interfaces it needs. It imports neither
`usage` nor `limits`.

A failed job and a cancelled job draw a record rather than no record. The work
is a real event in the account's history. A spend report that showed only the
jobs that succeeded would answer "what did this account do" with a shorter list
than the truth.

`TestAFailedJobDrawsNoCost` and `TestACancelledJobDrawsNoCost` assert the state
and the flag separately. An account reading its own history needs to tell a
provider that broke from a caller that changed its mind.

`ErrorClass` stays empty on a failed job's record. `internal/jobs` holds a
caller-facing reason string and no failure kind. The `internal/failure`
vocabulary is out of reach from a leaf that owns job state. A class invented at
the accounting seam would be a guess an operator could not act on.

## A video costs one video price

Starmap prices a video per video. It prices no video per second and none per
token. The video count is therefore the whole meter for the operation.
`usage.Media` grows `GeneratedVideos`, and `mediaCost` reads
`Operations.VideoGen`.

An offering that serves videos and publishes no video price withdraws the whole
cost. It does not report the token half alone.
`TestAnOfferingThatPricesNoVideoWithdrawsTheWholeCost` states why. A video is
the most expensive unit this gateway meters. Reporting the tokens alone would
read as the bill and understate it by orders of magnitude.

`usageCost` also had to stop treating a token count of zero as no usage.
`TestAVideoAloneIsUsage` covers that gap. A video carries no token count at
all. The old guard reported every finished video as a turn the provider never
metered.

The price comes from the catalog the job ended under. It does not come from the
one that routed the submission. A record cannot hold a snapshot, and a poll may
land minutes later. `JobAccountant` therefore reads the catalog through a
closure. The alternative is no price at all. A price from a catalog that moved
between the two is closer to the truth than that.

## The bound meters work in flight

Every other limit in this gateway meters something that is already over: a
request that returned, or bytes that are already stored. An outstanding job is
a spend commitment this gateway already made to a provider and cannot read yet.
It is the only bound that can refuse a caller before the provider bills for it.

`limits.JobMeter` wraps the same level meter `StorageMeter` wraps. The two stay
separate types on purpose. Both satisfy `jobs.Meter` structurally. Merging them
would let a deployment count videos against its stored byte bound and compile.
Both files carry a `dupl` suppression stating that reason.

`TestConcurrentSubmissionsCannotBothPassABoundThatAdmitsOne` is the property
that matters and that no other test in the package covers. A meter that read
the total, decided, and then wrote would admit two simultaneous submissions.
Both would read a total that admits them, and both would start provider work.

`tenant.DefaultOutstandingJobs` is eight. It is a working number for one
operator, not a provider fact. A video takes minutes, so eight in flight keeps
an interactive caller from waiting. It stays far under what a provider queues.

`TightestOutstandingJobs` resolves an account bound against a key bound the way
stored bytes resolve. Both read one counter, and the smaller of them satisfies
the larger.

## The claim comes before the route

`Service.Submit` now takes an `OpenRunner` rather than a `Runner`. Building a
runner resolves a route and a credential. An account already at its limit must
not spend either to learn it is at its limit. The claim happens first, and the
builder runs inside the call.

`TestASubmissionOverTheLimitReachesNoProvider` asserts the builder never ran.
It does not settle for asserting that the provider never ran.

Every path that then fails to reach a stored record gives the slot back. The
`kept` flag turns true immediately after `records.Create` succeeds, so `settle`
cannot release a slot the unwinding defer already released.
`TestARefusedProviderGivesTheSlotBack` covers the gap between the two. That gap
is the worst place to leak. A provider outage would otherwise walk an account
into its own limit and keep it there.

## The sweep makes the bound temporary

Every other path settles a job because a caller polled it. A caller that
submits and walks away is exactly the caller the limit exists for. Without a
sweep, one abandoned job holds one slot for as long as the process runs.

`Sweep` therefore ends a job past its polling budget through `policy.FailSpent`,
which needs no runner. It then settles any terminal job that carries no stamp.
`SweepResult` reports `Accounted` alongside `Expired` and `Abandoned`, so an
operator reads which half of the pass did the work. The early return for a
deployment with no byte store moved inside `sweepOne`. Left where it was, the
accounting half would never run there.

`TestTheSweepClosesAJobNobodyCameBackFor` runs the pass twice. The second pass
finds nothing. That is what stops a sweep on a ticker from drawing a record per
tick for the rest of the day.

## The refusal a caller actually reads

`TestAnAccountAtItsOutstandingJobLimitReadsARefusal` drives the router. It
holds three halves, because none proves the surface alone. A meter test proves
the arithmetic and says nothing about the status a caller reads. A controller
that mapped the refusal to 500 would still pass every test in
`internal/limits`. A refusal that arrived after the gateway resolved a
credential would bound what the account reads rather than what it spends.

The test server routes no video, so a submission that gets past the limit reads
503. Reading 503 proves the limit admitted the submission and the router saw it.
Reading 429 before it proves the limit answered first. The body carries the
number that refused, because an operator raising a limit needs it.

`TestAnAccountThatStatesNoOutstandingJobLimitStillHasOne` keeps the default on
the request path. A deployment whose operator set no limit is not unbounded.

## Evidence

```
go build ./...                                   clean
go vet ./...                                     clean
go test ./...                                    all packages ok
make lint                                        0 issues
bash scripts/verify-async-media-jobs.sh          Summary: 16 passed, 2 failed
bash scripts/verify-v1-architecture.sh           Summary: 12 passed, 0 failed
bash scripts/verify-dependency-direction.sh      Summary: 6 passed, 0 failed
bash scripts/verify-package-layout.sh            passed
bash scripts/verify-files-api.sh                 Summary: 22 passed, 0 failed
bash scripts/verify-model-modalities.sh          Summary: 26 passed, 0 failed
bash scripts/verify-auth-onboarding.sh           Summary: 26 passed, 0 failed
```

The two failures are `AMJ-V17` and `AMJ-V18`. AMJ8 and AMJ9 own them.

New tests: 7 in `internal/jobs/accounting_test.go`, 6 in
`internal/limits/outstanding_test.go`, 7 in
`internal/proxy/job_accounting_test.go`, and 2 in
`internal/server/video_limit_test.go`.
