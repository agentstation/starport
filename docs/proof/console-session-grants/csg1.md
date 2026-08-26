# CSG1 — The grant seam

## Problem

`Gate.Redeem` was the only caller of `IssueSession`. A second way into a
console session had nowhere to attach, so each new one would have copied the
cookie handling and drifted from it.

## Change

`internal/localauth/grant.go` (new) names the three kinds — `ticket`,
`local-token`, `identity` — and defines a `Grant` interface whose single
`Mint` method turns a `GrantRequest` into a session. `Gate` holds a
`map[GrantKind]Grant` and files each grant under the kind it reports, so a
grant cannot be registered under a name it does not claim. `Gate.MintSession`
is the one path a caller uses; `Gate.Redeem` keeps its signature and delegates
to `MintSession(GrantTicket, …)`.

`Session` gained a signed `Grant` field, carried in the cookie payload as
`"g"`. `IssueSession` takes the kind and refuses one it does not know.
`VerifySession` refuses a cookie whose grant name this binary does not
register.

Only the ticket grant is registered by this task. `local-token` arrives in
CSG2 and `identity` in CSG3, and until then asking for either returns
`ErrGrantUnknown` rather than panicking — which is the property that makes an
inert grant shippable at all.

## Why `GrantRequest` and not a bare claim

The first shape of `Mint` took a claim string. That is enough for a ticket,
which is single-use, short-lived, and was handed to the browser by a process
on this machine — where the browser then presented it adds nothing.

It is not enough for the pasted token. `internal/app/app.go:563` already
refuses to start when `AllowsExposure(config.Server.Host, token)` is false, so
by the time a `Gate` exists the *configured* bind is either loopback or the
token is rotated. A reverse proxy in front of a loopback gateway defeats that:
startup saw `127.0.0.1` and allowed it, and the paste route is then reachable
from the network with a first-boot secret that has been sitting in a terminal.

The honest control is where the request came from, not where the process
bound. `GrantRequest` carries `RemoteHost` — a host string, not an
`*http.Request`, so the package does not become a second HTTP layer — and an
empty value means an in-process caller. CSG2 uses it.

## Tests

`internal/localauth/grant_test.go`, four cases:

| Test | Holds |
| --- | --- |
| `TestATicketMintedSessionRecordsItsGrant` | The kind survives the round trip through the signed cookie. |
| `TestASessionNamingAnUnknownGrantIsRefused` | A correctly signed cookie naming a grant this binary does not register is refused, and so is one with no grant at all. |
| `TestAnUnregisteredGrantRefuses` | Both `Gate.Grant` and `Gate.MintSession` return `ErrGrantUnknown` for an unregistered kind. |
| `TestAGrantIsFiledUnderTheKindItClaims` | Every registry key equals the grant's own `Kind()`. |

The second is the one that earns its place. A valid signature proves this
machine's token minted the value and nothing more; a newer Starport could add
a grant with a narrower reach, and an older binary that read the cookie as
"some grant, close enough" would hand that browser the wide console. The
missing-grant case covers the same hole from the other side, where a zero
value would otherwise fall through.

## Fail-before

Two independent controls:

1. **Compile.** `TestATicketMintedSessionRecordsItsGrant` does not build
   against `20dd9b1`: `Session` has no `Grant` field.
2. **Negative control.** Removing the `knownGrantKind` guard from
   `VerifySession` and re-running:

   ```
   Error Trace: internal/localauth/grant_test.go:64
   Error:       Expected error with "the console session is not a session record" in chain but got nil.
   Test:        TestASessionNamingAnUnknownGrantIsRefused/no_grant_at_all
   FAIL
   ```

   Restored after the run.

## Checks

| Check | Result |
| --- | --- |
| `bash scripts/verify-console-session-grants.sh` | `Summary: 4 passed, 12 failed` — matches the CSG1 target |
| `go test ./... ` | all packages ok |
| `go test ./internal/localauth/... -race` | ok |
| `go vet ./...` | clean |
| `make lint` | 0 issues |
| `bash scripts/verify-auth-onboarding.sh` | 26 passed, 0 failed |
| `bash scripts/verify-package-layout.sh` | passed |
| `bash scripts/verify-dependency-direction.sh` | 6 passed, 0 failed |
| `bash scripts/verify-v1-architecture.sh` | 12 passed, 0 failed |

## Call sites updated

`IssueSession` gained a parameter, so its two test callers outside this file
were updated to pass `GrantTicket`:
`internal/localauth/browser_test.go` (5 calls) and
`internal/server/session_middleware_test.go` (2 calls). No production caller
outside `internal/localauth` existed.
