# AMJ6 Asset retention

## Outcome

A finished video now reaches the caller from Starport. The gateway fetches the
asset once and stores the bytes through `internal/blob`. The job records their
size and media type. The content route serves them. An asset past its window
goes, its job keeps an expired marker, and the route answers 410 with the
window in the body.

The gate moves from 11 of 18 to 13 of 18. `AMJ-V12` and `AMJ-V13` pass, which
closes phase D.

## The content route never redirects

A provider serves a finished video from a link that expires and that carries
the provider's own credential. A caller holding a Starport job identifier
cannot follow such a link, and this gateway cannot promise how long it lasts.

So the route serves Starport's own bytes.
`TestVideoContentServesStarportBytes` asserts the media type, the length, and
the exact body. It then asserts the answer is not `http.StatusFound` and that
no `Location` header exists. A redirect is the shape this route exists to
refuse, and it would pass a test that only asked whether the caller eventually
got bytes.

The same test asserts that neither the storage key nor the provider job
identifier appears in the response. The key addresses the byte store directly,
and the identifier is what invariant J1 keeps inside `internal/jobs`.

## Two fields, because expiry is two facts

The record holds `AssetExpiresAt` and `AssetExpiredAt`.

`AssetExpiresAt` is the promise. The record stores it rather than computes it
from a setting. An operator who shortens the window states what new jobs get.
`TestAJobKeepsTheWindowItWasPromised` asserts that a stored job keeps the end
it already holds.

`AssetExpiredAt` is the marker set when the bytes actually go. It is what
separates a completed job whose asset expired from a completed job that never
produced one. Those are different facts, and a caller acts on them differently:
one submits again, and the other collects sooner.

`ExpireAsset` never moves the job state. A completed job stays completed after
its asset expires. The work happened, and the tenant paid for it. AMJ7 prices
from that state. A job that fell back to a non-terminal word would either lose
its cost record or draw a second one.

## The read decides expiry

`Open` refuses an asset past its window before it reads a byte, and reclaims it
there. `TestAnAssetPastItsWindowIsRefusedBeforeTheSweepRuns` states the rule.

The sweep runs on an interval. An asset that answered for the length of that
interval past its stated window would make the window a suggestion. This
follows what `internal/files` already does for a stored file.

`expire` deletes the bytes before it marks the record. An interrupted pass then
leaves a record naming bytes that may already be gone, which the next pass
finishes. The other order leaves an object no record names, and nothing can
ever find it again.

## The fetch happens once

`TestAFinishedJobFetchesItsAssetOnce` polls a finished job five more times and
asserts one provider fetch. AMJ5 established that a caller may poll a finished
job without reaching a provider. The asset path holds the same property:
`AssetKey` stops the retry once the bytes land.

A failed fetch reports nothing. The work completed, so a reported failure would
name the wrong thing. The record stays truthful, the content route answers that
it holds nothing, and the next read tries again.
`TestAFailedFetchLeavesACompletedJobAndRetries` states both halves. The first
poll leaves a completed job with no asset, and the second collects it.

The retention window is what ends the retry if the bytes never arrive.

## The half that stores the bytes decides how large they may be

`jobs.Runner.Fetch` takes the byte bound as an argument. The connector reads
`io.LimitReader(body, bound+1)` and refuses a body over the bound. Reading one
byte past is what separates an asset exactly at the bound from one over it.

A connector with a setting of its own would let the provider layer size this
deployment's storage. The test `TestAFinishedJobFetchesItsAssetOnce` asserts
that the bound the service holds reaches the runner. A regression to a provider-side
setting then fails rather than passes quietly.

## The transport reuses the media route

`RouteVideoContent` runs through `routeMedia` exactly as a poll does, pinned to
the provider that accepted the job. A second provider does not hold the asset,
so the fan-out every other media operation relies on has nothing to reach here.

Two routes already held that pin. `acceptedJobPolicy` now owns it, and the
poll, the cancel, and the content read all call it. A key whose provider
restriction no longer names the accepting provider reads no models available.
That is the same answer it gets for a model it may not use.

`connectors.JobRunner` gained a fourth method rather than `Connector` gaining
one. Invariant J5 protects `Connector`. `JobRunner` is the optional interface
AMJ0 introduced for exactly this seam.

`JobAsset` carries a real deep `Clone`, because `routeMedia` clones an answer
on the retry path. A shallow copy would hand two attempts the same slice.

## The asset is not cached twice

`cachedService.FetchVideoAsset` passes straight through. The record store holds
the bytes after the call. A second copy in the response cache would hold a
whole video for a window nothing sized.

## Storage keys name nothing

`newAssetKey` is `hex(uuid)`, not derived from the job identifier. A leaked
identifier therefore names nothing in the byte store. This follows what
`internal/files` does for a stored file, and it satisfies the flat key charset
`blob.ValidateKey` allows.

## Three settings an operator reads

`STARPORT_JOBS_ASSET_RETENTION` defaults to a day. That is short beside the
file store's month on purpose. A generated video is an answer a caller collects, not a document it keeps.
Both provider families publish links of their own that last hours.

`STARPORT_JOBS_MAX_ASSET_BYTES` defaults to 256 MiB.

`STARPORT_JOBS_SWEEP_INTERVAL` defaults to an hour. It is a floor on how long
expired bytes survive on disk, not on how long an asset reads.

`internal/config` and `internal/jobs` each state their own default, which is
what `internal/config` and `internal/files` already do. The seam has to open
without a config package, and the config package has to answer an absent
setting without a seam.

## The byte store is opt-in

A service built without one collects nothing and sweeps nothing.
`TestAJobWithNoByteStoreCollectsNothing` asserts both. The test server now
composes one. A route test that omitted it would give a gateway that fetched an
asset the same answer as one that never did.

## Commands

```
go test ./...                                  ok, exit 0
go vet ./...                                   clean
make lint                                      0 issues
make build                                     ok
bash scripts/verify-async-media-jobs.sh        Summary: 13 passed, 5 failed
bash scripts/smoke-openrouter-sdks.sh          3 SDKs pass
```

The seven packages this task touched run 791 tests: 228 in
`internal/providers/connectors`, 188 in `internal/server`, 113 in
`internal/config`, 79 in `internal/proxy`, 75 in `internal/router`, 57 in
`internal/app`, and 51 in `internal/jobs`.

Twenty standing gates ran clean beside them.

| Gate | Result |
| --- | --- |
| Starmap ownership | 12 passed |
| v1 architecture | 12 passed |
| dependency direction | 6 passed |
| dependency direction verifier | passed |
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
| release workflow | passed |
| README quickstart | passed |
| documentation links | passed |
| documentation link verifier | passed |
| action pins | 16 references |

The overhead benchmark reports p50 and p99 at 0 ms over 200 requests.

## Gate movement

```
PASS AMJ-V12 the content route serves Starport bytes and never a provider URL
PASS AMJ-V13 an expired asset answers 410 and its job keeps an expired marker
```

The five failures that remain belong to AMJ7 through AMJ9.
