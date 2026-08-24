# AON5 Tenant governance

Date: 2026-08-23
Plan: [Auth and Onboarding](../../plans/auth-and-onboarding-plan.html)
Verifier: `scripts/verify-auth-onboarding.sh`, AON-V17 and AON-V18

## What changed

AON4 gave the operator a route for applying one provider credential to the
whole deployment. Nothing bounded what an account could then spend through it.
`Tenant.Limits` existed and no enforcement path read it, so an operator could
share a gateway credential and had no way to say how much of it any account may
use. AON5 makes the account a governed thing: an operator creates, caps, and
retires one over HTTP, and both enforcement paths meter it.

| Surface | Route | Guard |
| --- | --- | --- |
| Account plane (operator) | `/api/v1/admin/tenants[/{tenant_id}]` | `admin` |
| Account usage rollup | `/api/v1/tenants/{tenant_id}/usage/providers` | tenant access + `activity:read` or `admin` |

## The deviation: both meters, not the stricter one

The plan's step 2 said to resolve the effective limit as "the stricter of the
tenant limit and the key limit". That rule is unsound, and AON5 does not
implement it.

An account limit and a key limit are not two candidate values for one meter.
They meter different populations: the account meter counts every key the
account holds, and the key meter counts one key. Resolving them to whichever
number is smaller lets an account with N keys spend N times its own cap,
because each key stays inside the smaller value on its own meter and nothing
ever totals them. An account capped at 1 request per minute holding ten keys
would serve ten.

So a request satisfies **every** rule that applies to it. `internal/limits/rules.go`
owns that rule, and no enforcement path re-derives it:

```go
func RequestRules(tenantLimits, keyLimits *Limits, deploymentDefault *RequestLimit) []RequestRule
func BudgetRules(tenantLimits, keyLimits *Limits, dimension Dimension) []BudgetRule
```

Each rule carries the `Scope` that set it, so a refusal can name the holder an
operator has to talk to. Rules arrive account-first and every caller stops at
the first refusal: the reverse order spends an account token on a request the
key cap then refuses, and a consumed rate token is never returned.

The doc comment on `Tenant.Limits` had encoded the wrong semantics since AON1
("a key limit may lower it and never raise it"). It now states the rule the
code enforces.

## Storage: an account is a counter population

`usage.Totals` took a key ID string where `""` meant "every key". Three
populations shared one parameter and one of them was spelled as the absence of
another. `usage.Scope` replaces it:

```go
func KeyScope(keyID string) Scope
func TenantScope(tenantID string) Scope
func GatewayScope() Scope
```

It is comparable, so a test can key a map by it, and a scope that names no
subject is `ErrInvalidScope` rather than a silent read of the gateway total —
which would have reported every account's spend as one account's.

`accumulate` now advances three counter sets per record: the key's, the
gateway's, and the account's. `usage.Record` gained `TenantID`, and
`internal/proxy/usage_capture.go` fills it on every record, falling back to
`"anonymous"` so unauthenticated traffic stays out of a real account's total
while remaining visible in its own.

`Record.TenantID` is deliberately **not** required by `Validate()`. Requiring
it would make `decodeRecord` reject every record already written, and a record
that names no account simply counts toward none.

`Query` gained a `TenantID` filter. Records are key-indexed, so an account
query scans and filters; that cost belongs to reporting, and budget enforcement
reads the aggregate counters instead.

## Step 5 needed no work

The plan asked to keep account resolution off the hot path's storage. AON3 and
AON4 already did it: `RequireAPIKey` puts both the tenant ID and the tenant
record on the request context from the same cached lookup the key read uses, so
neither the rate-limit path nor the budget path touches storage to learn the
account.

## Reporting which meter bound the request

An operator seeing a 429 or a 402 has to know whether to raise the account cap
or the key cap. Every limited response now names the holder:

```text
X-RateLimit-Scope: tenant | key
X-Starport-Budget-Spend-Scope: tenant | key
X-Starport-Budget-Tokens-Scope: tenant | key
```

and the failure body says it too: `Rate limit exceeded: tenant request limit`,
`Insufficient quota: tenant spend budget exhausted for the current day window`.
The reported numbers come from the tightest meter, and an exhausted meter
always reports itself, because it is the one refusing the request.

## The account plane

`TenantsController` is a new controller rather than a growth of
`AdminController`, which already covers keys, system info, and metrics. An
account is a distinct concept with its own repository.

Two refusals are the substance of it:

- **The default account cannot be deleted.** Every key without an explicit
  account resolves to it; deleting it orphans all of them at once.
- **An account that still holds a gateway API key cannot be deleted.** The key
  would keep authenticating with no account behind it and would then run under
  the default credential policy — the operator would have revoked an account
  and silently widened what its keys reach. A key listing that cannot be read,
  or cannot be bounded, refuses for the same reason: an unproven answer here
  orphans a working credential.

`Update` is a read-modify-write at the revision it read, so a concurrent
operator edit is a 409 rather than a silent overwrite. `ID` is not writable: an
account ID reaches a credential storage scope and a usage counter, and renaming
it would orphan both.

`GET` and `POST` report `EffectiveCredentialStrategy()`, so a caller never has
to know that an unset strategy means the default one.

## The usage rollup moved

`/api/v1/keys/{key_id}/usage/providers` is now
`/api/v1/tenants/{tenant_id}/usage/providers`. Spend is an account question: a
caller reading one key's total has no way to know which other keys belong to
the same account. AON4 deferred this move here because the record carried no
account; it does now.

`RequireKeyOwnership` was the only guard on the old route and no route uses it
any more, so it is deleted along with its `Server` wrapper.

## Fail-before

On the AON4 head (`500dccb`), `internal/server/tenant_limits_test.go` does not
compile: `usage.Scope`, `usage.KeyScope`, and `usage.TenantScope` are undefined
at eight call sites, because no counter population but the key's existed. The
tests it holds state behavior nothing on that head could express — a request
refused by an account cap while every key stays inside its own.

## Tests

Sixteen new tests, one rewritten.

| Test | Package | What it states |
| --- | --- | --- |
| `TestTenantRequestLimitBindsBelowAKeyLimit` | `internal/server` | An account at 1/60s under a key at 100/60s binds at 1, reports `X-RateLimit-Scope: tenant`, and refuses the second request naming the account |
| `TestATenantRequestLimitCountsEveryKeyInTheAccount` | `internal/server` | Two keys each at 10/60s under an account capped at 1/60s: the second key's first request is refused, which the "stricter of" rule would have allowed |
| `TestAKeyLimitStillBindsUnderAGenerousTenant` | `internal/server` | A key at 1/60s under an account at 100/60s refuses at key scope while a sibling key is still served |
| `TestTenantSpendBindsOnTheAccountTotal` | `internal/server` | A key well inside its own spend budget is refused on the account total, with the spend scope header and remaining 0 |
| `TestAKeyBudgetStillBindsUnderAGenerousTenant` | `internal/server` | The symmetric case reports scope `key` |
| `TestRequestRulesKeepBothMetersAndOrderAccountFirst` | `internal/limits` | Two holders produce two rules, account first |
| `TestTheDeploymentWindowOnlyFillsInForAKeyThatSetsNone` | `internal/limits` | The gateway's global window fills in for a key that sets none and never overrides one that does |
| `TestBudgetRulesSelectOneDimensionPerHolder` | `internal/limits` | An account capping only spend does not silently cap tokens; an unknown dimension bounds nothing |
| `TestAccountCounterSumsEveryKeyItHolds` | `internal/usage` | The account counter is the sum over every key it holds, and one account's traffic never reaches another's |
| `TestListByAccountSpansEveryKey` | `internal/usage` | An account query returns every key's records and no other account's |
| `TestDeletingAnAccountThatStillHoldsKeysIsRefused` | `.../controllers` | The delete is refused with 409 while a key names the account, the account survives to be reassigned, and the same delete succeeds once no key does |
| `TestDeletingTheDefaultAccountIsRefused` | `.../controllers` | 409 on the canonical account |
| `TestAccountUpdateIsRevisionChecked` | `.../controllers` | A competing write between the read and the write answers 409, not last-write-wins |
| `TestCreatedAccountReportsTheStrategyItRunsUnder` | `.../controllers` | A new account is active, is reported with its effective strategy, and a duplicate ID is 409 |
| `TestAccountPlaneWithoutStorageRefusesRatherThanReportingNoAccounts` | `.../controllers` | No account storage answers 503, never an empty list |
| `TestActivityByProviderExcludesOtherAccounts` | `.../controllers` | An account with no records reports none while another account's records sit in the same window |
| `TestActivityByProviderAggregates` (rewritten) | `.../controllers` | The rollup spans two keys under one account and excludes a third account's key |

`TestPutRejectsInvalidRecords` gained one case: a scope naming no subject is
`ErrInvalidScope`.

## Repository gates

```text
go build ./...                                     clean
go test ./...                                      no failures
go test -race ./internal/server/... ./internal/limits/... ./internal/usage/...  no failures
go vet ./...                                       clean
make lint                                          0 issues
make build                                         Build complete: ./starport
bash scripts/verify-starmap-ownership.sh           Summary: 12 passed, 0 failed
bash scripts/verify-v1-architecture.sh             Summary: 12 passed, 0 failed
bash scripts/test-dependency-direction-verifier.sh passed
bash scripts/verify-dependency-direction.sh        Summary: 6 passed, 0 failed
bash scripts/verify-catalog-driven-providers.sh    Summary: 19 passed, 0 failed
bash scripts/verify-package-layout.sh              package-layout verification passed
bash scripts/verify-readme-quickstart.sh           passed
bash scripts/verify-v1-release.sh                  Summary: 16 passed, 0 failed
bash scripts/verify-release-workflow.sh            passed
bash scripts/verify-developer-experience.sh        Summary: 47 passed, 0 failed
bash scripts/verify-doc-links.sh                   passed
bash scripts/test-doc-link-verifier.sh             passed
bash scripts/verify-openrouter-parity.sh           Summary: 16 passed, 0 failed
bash scripts/verify-console-modernization.sh       Summary: 21 passed, 0 failed
bash scripts/verify-catalog-performance.sh         Summary: 20 passed, 0 failed
bash scripts/verify-action-pins.sh                 16 reference(s) match their release tags
bash scripts/benchmark-overhead.sh                 p50=0ms p99=0ms
bash scripts/smoke-openrouter-sdks.sh              PASS Python, TypeScript, Go
bash scripts/verify-auth-onboarding.sh             Summary: 18 passed, 8 failed
```

AON-V17 was re-pointed from `Admin.ListTenants` to `Tenants.List`: the account
plane is its own controller, and the condition names a symbol rather than a
path so the BYOK plane's `/tenants` prefix cannot turn it green by accident.
AON-V18 now names `TenantScope`, which is the account counter the budget path
actually reads.

## What AON5 deliberately did not do

- No console screen manages accounts. The routes exist and are admin-guarded;
  the console work belongs to AON7 and AON10.
- Authentication is still unconditionally required. AON6 owns the mode.
- An account's spend is metered but not billed: there is no invoice, no
  prepayment, and no hard stop other than the 402.
