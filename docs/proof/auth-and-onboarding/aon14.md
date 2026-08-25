# AON14 The served credential source

Date: 2026-08-25
Plan: [Auth and Onboarding](../../plans/auth-and-onboarding-plan.html)
Verifier: none. This task adds no condition to
`scripts/verify-auth-onboarding.sh`; its claims are behavioral and are held by
tests at four seams.

## What changed

AON10 gave each credential idea one screen. A person can now see, on the
provider screen, that an environment credential is present and that a gateway
credential is applied. What they could not see is which of them ever paid for
anything.

The gateway already chose a plane per attempt and already knew which one it
chose. It threw the fact away at the end of the attempt. This task carries it
from the choice to the record, and shows the window's breakdown on the screen
that shows the planes.

```text
credentialPolicy.resolve   picks a plane   → keyring.CredentialSource
execution.AttemptEvidence  keeps it        → Credential.Source, per attempt
router.Response            reports it      → CredentialSource
usage.Record               stores it       → credential_source
GET /api/v1/activity       serves it       → credential_source
/providers/{id}            shows it        → "Paid by (1h)"
```

## Per attempt, not per request

A request can change planes mid-flight: the environment credential is offered,
the provider refuses it, and the operator's applied gateway credential serves
the retry. The plane is therefore recorded on the attempt, beside the route and
the failure, and the request-level field is read off the attempt that answered.

`servedAttempt` finds that attempt — the last one availability did not skip —
and both `ProviderUsed` and `CredentialSourceUsed` read from it. Reading them
from different attempts would let a record name a plane that never served the
provider it names.

## Owner and source are different facts

`execution.CredentialEvidence` already carried an `Owner`, and execution acts
on it: an attempt paid for by a tenant must not move the shared availability
state. It does not act on the plane, so the plane is carried beside the owner
as an opaque string the router fills in.

That keeps the credential vocabulary in `internal/providers/keyring`, which
owns it. Execution does not learn the words `environment`, `gateway`, or
`byok`, and there is no second copy of the vocabulary to drift.

One word is new. `keyring.SourceAnonymous` names an attempt a provider accepted
with no credential at all. No strategy selects it and no plane holds it; it
exists so a record can say "nothing paid for this" instead of saying nothing,
which is what an unrecorded request also says.

## What the tests hold

| Seam | Test | Claim |
| --- | --- | --- |
| `internal/router` | `TestServedCredentialSourceNamesThePlaneThatPaid` | one request per plane reports that plane |
| `internal/router` | `TestServedCredentialSourceFollowsTheFallback` | a refused environment credential reports `gateway`, not `environment` |
| `internal/proxy` | `TestUsageRecordCarriesTheServedCredentialSource` | the plane reaches `usage.Record` on both the buffered and streaming paths |
| `internal/server/controllers` | `TestActivityResponseNamesTheCredentialSource` | the `credential_source` key crosses the wire |
| `console` | `ServedCredentialPanel.test.tsx` | a window served by more than one plane names each, with counts |

The activity test reads the raw JSON rather than decoding into `usage.Record`,
because decoding into the same struct that wrote the record would pass even if
the field never crossed the wire. The console reads the key by name, so the key
is the contract.

The console panel counts a record with no source as `Unrecorded` rather than
folding it into a plane. Attributing spend to a plane that may not have paid is
worse than admitting the record predates the field.

## Fail-before

The plan recorded the baseline as `grep -rn CredentialSource internal/usage`
returning nothing on the AON10 head:

```console
$ git grep -n CredentialSource 4e3fde5 -- internal/usage
$ echo $?
1
```

The fallback case fails on a plausible wrong implementation. Reading the first
attempt instead of the served one — `servedAttempt` iterating forward rather
than backward — reports the plane the policy reached first:

```console
$ go test ./internal/router/ -run TestServedCredentialSource
--- FAIL: TestServedCredentialSourceNamesThePlaneThatPaid/gateway
    expected: "gateway"
    actual  : "environment"
--- FAIL: TestServedCredentialSourceFollowsTheFallback
    expected: "gateway"
    actual  : "environment"
FAIL
```

## Deviation

The plan's step 4 said to leave `CredentialStatus` reporting the environment
plane it already reports. It does. The provider screen now carries three
panels rather than two: environment, gateway, and what actually paid. The plan
said "beside the two credential panels", and beside is literal — the grid is
three columns on wide screens.

## Evidence

```console
$ go build ./... && go vet ./... && gofmt -l internal/
$ go test ./...
all packages pass

$ make lint
0 issues.

$ make build
Build complete: ./starport

$ bash scripts/verify-auth-onboarding.sh
Summary: 26 passed, 0 failed

$ bash scripts/verify-dependency-direction.sh
Summary: 6 passed, 0 failed

$ bash scripts/verify-console-modernization.sh
Summary: 21 passed, 0 failed

$ cd console && npx tsc --noEmit
$ npx vitest run
 Test Files  16 passed (16)
      Tests  88 passed (88)

$ npm run build
✓ built
```

New tests: 4 Go cases across three packages, 3 console cases. No test was
weakened or removed.
