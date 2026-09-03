# CAT8 The Starport catalog runtime

Starport now reads one connected Starmap runtime. A deployment names a source
kind, the runtime keeps a candidate, Starport validates the candidate against
its own routes, and the accepted head advances under a lease epoch. An operator
starts a refresh and reads the catalog state through two routes with two
different trust levels.

## Fail before

Every condition below failed on the base commit `117ad8f5`, because the named
test did not exist.

| Condition | State before | Command |
| --- | --- | --- |
| CAT-V42 | `TestRemovedCatalogVariableFailsStartup` absent | `go test ./internal/config -list ...` printed no name |
| CAT-V43 | `TestStreamingCarriesNoElapsedDeadlineAfterFirstByte` absent | `go test ./internal/execution -list ...` printed no name |
| CAT-V44 | `TestAdminRefreshReturnsAcceptedOperation` absent | `go test ./internal/server/controllers -list ...` printed no name |
| CAT-V45 | `TestAcquisitionResolverReadsOnlyDeploymentLookup` absent | `go test ./internal/catalog -list ...` printed no name |
| CAT-V46 | `TestRemoteRuntimeAcceptsOnlyMatchingForwardState` absent | `go test ./internal/catalog -list ...` printed no name |
| CAT-V48 | `TestAcceptRejectsStaleLeaseEpoch` absent | `go test ./internal/catalog -list ...` printed no name |
| CAT-V51 | all three server tests absent | `go test ./internal/server -list ...` printed no name |
| CAT-V62 | `TestAdminCatalogStatusReportsRouteValidationState` absent | `go test ./internal/server -list ...` printed no name |

CAT-V47 passed before the task and still passes. The task kept
`internal/catalog/freshness.go` unchanged, and it moved the manifest detail
behind the admin scope rather than dropping it.

## What was built

**One connected runtime.** `internal/catalog/runtime.go` replaces the
local-or-remote choice with one composition. `OpenRuntime` states the
credential plane and delegates to `openRuntime`, which holds the whole wiring:
the candidate store, the accepted store, the lease store, the source kind, and
the acquirer. A private source never falls back to the public channel, because
the selected kind reaches Starmap exactly as the operator named it.

**Canonical settings.** `internal/config/catalog.go` reads the
`STARPORT_CATALOG_` names. Each removed variable refuses startup and names its
replacement. `internal/config/credential_sources.go` holds the credential-source
settings, so `config.go` names no removed variable and the CAT-V42 grep guard
reads one file.

**The acquisition credential plane.** `internal/catalog/acquisition_resolver.go`
holds the deployment lookup and nothing else. A structural check asserts the one
field, so the resolver can reach no keyring, no account store, and no BYOK
record.

**The lease fence.** `internal/catalog/lease.go` gives one holder per deployment
and raises the epoch on every fresh acquisition.
`internal/catalog/acceptance.go` refuses a candidate that an older epoch
produced, a payload whose checksum does not match, and a catalog that does not
move forward. The commit is an expected-ID compare-and-swap, so two instances
cannot commit the same head.

**Route validation.** `starmap.RuntimeStatus` carries no candidate state, so
Starport owns `internal/catalog/validation_record.go`. The accepted head stays
durable in the generation store; the record says only how this instance reached
it. Its four states are distinct: no candidate, pending, accepted, and refused.

**Asynchronous refresh.** `internal/catalog/operations.go` keeps one open
operation per kind. An overlapping refresh joins the run in flight and reads the
same operation. `ClassifyOperationFailure` maps every failure onto one closed
reason set, so the admin status, the audit subject, and the log field carry no
provider text and no URL.

**Two catalog routes with two trust levels.** `AdminStatus.Summary()` in
`internal/catalog/status.go` names each served field, so a new admin field never
reaches a reader by default. `App.CatalogSummary` derives from
`App.CatalogStatus`, so the two surfaces cannot drift. The admin status route
requires the admin scope.

**Route-specific timing.** `internal/execution/stream.go` bounds route selection
alone and releases that bound at the first byte, so a committed stream carries a
cancelable lifetime and no elapsed deadline.

## Tests

| Test | Package | Condition |
| --- | --- | --- |
| `TestRemovedCatalogVariableFailsStartup` | `internal/config` | CAT-V42 |
| `TestStreamingCarriesNoElapsedDeadlineAfterFirstByte` | `internal/execution` | CAT-V43 |
| `TestAdminRefreshReturnsAcceptedOperation` | `internal/server/controllers` | CAT-V44 |
| `TestAcquisitionResolverReadsOnlyDeploymentLookup` | `internal/catalog` | CAT-V45 |
| `TestRemoteRuntimeAcceptsOnlyMatchingForwardState` | `internal/catalog` | CAT-V46 |
| `TestAcceptRejectsStaleLeaseEpoch` | `internal/catalog` | CAT-V48 |
| `TestLeaseFencesOneHolderPerDeployment` | `internal/catalog` | CAT-V49 |
| `TestSafeCatalogRouteProjectsAllowlistedSummaryOnly` | `internal/server` | CAT-V51 |
| `TestSafeCatalogRouteAnswersMissingCatalogWithSanitized503` | `internal/server` | CAT-V51 |
| `TestAdminCatalogStatusRequiresAdminScope` | `internal/server` | CAT-V51 |
| `TestAdminStatusSeparatesEveryOperationalValue` | `internal/catalog` | CAT-V60 |
| `TestAdminCatalogStatusReportsRouteValidationState` | `internal/server` | CAT-V62 |
| `TestStarmapAcquisitionPublishesRefresh` | `internal/catalog` | ownership O03 |
| `TestVerifiedRemoteCatalogActivatesProvider` | `internal/app` | CDP-V17 |
| `TestOverlappingRefreshesJoinOneRun` | `internal/catalog` | supports CAT-V44 |
| `TestOperationsCloseTerminalStates` | `internal/catalog` | supports CAT-V44 |
| `TestOperationsBoundTheHistory` | `internal/catalog` | supports CAT-V44 |
| `TestOperationTimeoutClosesTheRun` | `internal/catalog` | supports CAT-V44 |
| `TestClassifyOperationFailureNamesASafeCause` | `internal/catalog` | supports CAT-V51 |
| `TestAdminStatusMapsEveryVocabularyOntoTheClosedSet` | `internal/catalog` | supports CAT-V51 |
| `TestValidationRecordReportsFourDistinctStates` | `internal/catalog` | supports CAT-V62 |
| `TestLeaseRefusesAnIncompleteRequest` | `internal/catalog` | supports CAT-V49 |

Every table above is table-driven where the condition has more than one case,
and every listed test runs under `-race`.

## Mutation evidence

Each mutation reverted after the run. The worktree was clean afterward.

| Mutation | Test that caught it | Observed failure |
| --- | --- | --- |
| `internal/server/controllers/catalog.go`: answer 200 instead of 202 | `TestAdminRefreshReturnsAcceptedOperation` | `expected: 202` in two subtests |
| `internal/catalog/status.go`: serve `Runtime.Lease` as the summary freshness | `TestSafeCatalogRouteProjectsAllowlistedSummaryOnly` | body `should not contain "instance-7.internal.example"` |
| `internal/catalog/acceptance.go`: skip `fenceEpoch` | `TestAcceptRejectsStaleLeaseEpoch` | `Expected error with "catalog candidate carries a stale lease epoch" in chain but got nil` in both subtests |
| `internal/catalog/operations.go`: return the raw failure text as the reason | `TestClassifyOperationFailureNamesASafeCause` | reason `should not contain "redact-me"` |
| `internal/catalog/runtime.go`: drop `starmap.WithAcquirer` | `TestStarmapAcquisitionPublishesRefresh` | the credential never resolved and the generation did not move |

## Commands

Every command ran with `GOTOOLCHAIN=go1.26.6` from the Starport worktree. The
Starmap verifier scripts read the plan worktree
`/Users/jack/src/github.com/agentstation/starmap-catalog-publisher`.

| Command | Result |
| --- | --- |
| `make lint` | 0 issues |
| `make format-check` | no output |
| `make test` | pass |
| `make test-race` | pass |
| `bash scripts/verify-starmap-ownership.sh` | 12 passed, 0 failed |
| `bash scripts/verify-v1-architecture.sh` | 11 passed, 1 failed (V01 only) |
| `bash scripts/verify-dependency-direction.sh` | 6 passed, 0 failed |
| `bash scripts/verify-package-layout.sh` | passed |
| `bash scripts/verify-catalog-driven-providers.sh` | 19 passed, 0 failed |
| `bash scripts/verify-catalog-distribution.sh` | 56 passed, 12 failed, 0 unverified |

`V01` fails because `go.mod` holds
`replace github.com/agentstation/starmap => …/starmap-catalog-publisher`. The
plan directs the build at the read-only plan worktree, so the replace stands
until the Starmap change publishes a version. `V01` is the one gate that reads
the module boundary, and it passes again when the replace goes.

The twelve failures the Starmap verifier reports are the console conditions
`CAT-V50`, `V52`, `V53`, `V54`, `V55`, `V63`, and `V68`, which CAT8.1 owns; the
Starport document conditions `CAT-V56`, `V57`, `V58`, and `V64`, which CAT9.1
owns; and the Starmap runbook condition `CAT-V59`. Every condition CAT8 owns
passes.

## Repairs outside the task

Two files carried formatting the pinned formatters refuse, and both blocked
`make format-check` before this task changed them.

- `internal/providers/state/store.go` needed `gofmt`.
- `internal/blob/objectstore.go` needed `goimports@v0.48.0`.

The task formatted both, so the format gate reports the change under test
instead of a defect it did not introduce.
