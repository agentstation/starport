# AMJ5 The caller routes

## Outcome

A caller now submits a video job, lists its own jobs, reads one, cancels one,
and asks for its content. Ten routes carry the surface: five under `/v1` and
five under `/api/v1`. Each protocol family answers in its own vocabulary. A job
another account owns answers not found.

The gate moves from 8 of 18 to 11 of 18. `AMJ-V09`, `AMJ-V10`, and `AMJ-V11`
pass, which closes phase C.

## One scope covers the whole surface

The five routes carry `videos:write` and no read scope. The account that
submits a job is the only account that can read it. A separate read scope would
name a capability no other caller can hold.

`DefaultAnonymousScopes` gained the same word. An operator who turned
authentication off to make a first request work would otherwise reach every
other surface and meet a refusal on this one.

`TestVideoRoutesCarryTheVideoScope` states the rule in both directions. A key
holding chat access alone reads 403 on all ten routes. A key holding
`videos:write` reaches the controller on all ten. The second half carries the
weight. A scope no key can satisfy also refuses every caller, so a route table
that demanded one would pass the first half alone.

## The route test walks the router

`TestServerRegistersTheVideoPaths` walks the router the server builds and
compares it against a written list of ten method and path pairs. This file holds the
list rather than reading it back from the router. A list the router supplied
would agree with itself.

A path spelled correctly in the route file and mounted under the wrong group
reads as present to a source scan. It still answers a caller with 404. Only the walk
separates the two.

The test server composes a real job service. A route test that omitted it would
read the unconfigured answer on every video path. It would prove nothing about
the surface.

## A job identifier discloses nothing

`TestVideoJobOfAnotherAccountIsNotFound` writes a job owned by `acme` that
holds the provider identifier `provider-side-identifier`. A `globex` key with
the correct scope then asks for it four ways.

Every answer is 404, not 403. A refusal would confirm that the identifier
exists. The identifier is the only thing such a caller can guess. The test also asserts that no response body contains the provider identifier.
That holds invariant J1 at the wire rather than at the record alone.

The same test then reads the job back through an `acme` key. Without that half,
the test would pass on a record no account could reach.

## Two families, two status vocabularies

The canonical record holds five words. Neither published family holds all five.
The two also disagree about the word for a job that has not started.

| Canonical | OpenAI | OpenRouter |
| --- | --- | --- |
| `queued` | `queued` | `pending` |
| `running` | `in_progress` | `in_progress` |
| `completed` | `completed` | `completed` |
| `failed` | `failed` | `failed` |
| `cancelled` | `cancelled` | `cancelled` |

OpenAI publishes no cancelled word. The codec keeps the canonical one rather
than borrowing `failed`. That word names something the caller did not do.

The two objects differ beyond the status word. The OpenAI object carries
`"object": "video"` and no provider. The OpenRouter object carries the serving
provider and no envelope word. Each listing follows its own family the same
way.

Two codec tests pin each shape against a decoded map rather than against the
struct. A struct assertion would pass after a rename of a JSON tag.

## The controller asks the gateway for its provider side

The first version of the controller built the job runner itself. The composition guard in `verify-v1-architecture.sh` reads that as
`internal/server` assembling a proxy internal. The guard is right. A runner is a
proxy-layer value that binds a transport and a credential policy to one
request.

`Proxy` gained a `VideoJobRunner` method instead. The controller asks the
gateway it already holds for the provider side of one caller's jobs. The caching wrapper answers with a runner bound to itself. A job that started
through the deployment's gateway therefore polls and stops through the same
one.

The method answers the `jobs.Runner` interface rather than the concrete value.
A typed nil pointer in a concrete return type would pass the absent-runner
check in `internal/jobs` and then fail on the first call.

## The record is the only thing that writes a state

`jobs.Service` sits between the routes and the store. Every state a caller reads passes through the transition table there. The
accounting rule and the retention rule that read the state later therefore see
one history.

`Submit` calls the provider before it writes the record. A record written first
would name a job no provider ever accepted. A caller polling it would wait out
the whole lifetime for an answer that never comes.
`TestSubmitWritesNoRecordWhenTheProviderRefuses` asserts that a refused
submission leaves the store empty.

`Refresh` answers a terminal job from the record and reaches no provider.
`TestPollingAFinishedJobReachesNoProvider` polls a finished job five times and
asserts the provider call count is zero. AMJ7 rests on that property. A caller polls until it sees a terminal answer and
usually once more after that. Each of those reads has to cost no provider
request, no use of the tenant's credential, and no second cost record.

`Cancel` moves the record on this gateway's own authority rather than on the
provider's answer. Take a provider that still reports the job as running one
moment after it accepted the stop. Reading that answer back would leave a job
this gateway stopped billing for still polling. A job that already ended answers `ErrJobAlreadyEnded`. A cancellation that
rewrote a completed job would discard both the asset and the cost record.

## The content route answers about the asset

`GET /v1/videos/{video_id}/content` reads the record first. A job another
account owns therefore answers not found there exactly as it does elsewhere.
The route then answers 404 with a message about stored content rather than
about the path.

AMJ6 owns storing the finished asset. The route exists now so that the answer
names the missing thing.

## One composition fix outside the task

Adding the job service pushed `openConcepts` in `internal/app` to a cyclomatic
complexity of 31 against a ceiling of thirty. Four steps moved into
`openAccountIdentity`: the tenant repository, the identity repository, the
authentication mode, and the local token. They belong together. A gateway API key names an identity, and an identity
resolves to a tenant. The authentication mode then decides whether a deployment
may hold no identity at all.

## Commands

```
go test ./...                                  ok, exit 0
go vet ./...                                   clean
make lint                                      0 issues
make build                                     ok
bash scripts/verify-async-media-jobs.sh        Summary: 11 passed, 7 failed
bash scripts/smoke-openrouter-sdks.sh          3 SDKs pass
```

The five packages this task touched run 385 tests: 185 in `internal/server`,
79 in `internal/proxy`, 45 in `internal/jobs`, 39 in `internal/protocol/openai`,
and 37 in `internal/protocol/openrouter`.

Sixteen standing gates ran clean beside them.

| Gate | Result |
| --- | --- |
| Starmap ownership | 12 passed |
| v1 architecture | 12 passed |
| dependency direction | 6 passed |
| package layout | passed |
| catalog driven providers | 19 passed |
| OpenRouter parity | 16 passed |
| model modalities | 26 passed |
| files API | 22 passed |
| auth onboarding | 26 passed |
| console session grants | 16 passed |
| console modernization | 21 passed |
| catalog performance | 20 passed |
| developer experience | 47 passed |
| v1 release | 16 passed |
| documentation links | passed |
| action pins | 16 references |

The overhead benchmark reports p50 and p99 at 0 ms over 200 requests.

## Gate movement

```
PASS AMJ-V09 a route test walks the router and names the four video paths
PASS AMJ-V10 a key holding no videos:write scope cannot submit a job
PASS AMJ-V11 another account's job answers not found rather than forbidden
```

The seven failures that remain belong to AMJ6 through AMJ9.
