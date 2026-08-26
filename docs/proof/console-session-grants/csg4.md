# CSG4 — The HTTP route

## Problem

The `local-token` grant existed in Go and no browser could reach it.

## Change

`internal/server/controllers/console_session.go` (new) serves
`POST /console/session`. It reads a JSON body, invokes the `local-token` grant
with the caller's peer address, and sets cookies through
`localauth.SessionCookies` — the same call the launch controller uses, so both
grants produce the same session.

Registered in `internal/server/routes.go` beside `/launch`, outside every
authentication group. A caller presenting a credential to get a session has by
definition not got one yet.

Success is 204 with no body: everything the browser needs is in the cookies,
and a body that echoes anything about the credential is a body that ends up
somewhere. Refusal is 401 with a JSON message, and it clears both session
cookies.

## Why the token is in a JSON body

A query string is in the address bar, in browser history, in the referrer of
the next request, and in the access log line that records the URL. A launch
ticket survives that because it is single-use and lives about a minute. The
local admin token is neither.

## Why `RemoteAddr` and not the middleware's client IP

The grant judges where the caller is, and on this route that answer decides
whether an unrotated first-boot secret is accepted. Two things follow.

A forwarded header is written by whoever is upstream, so a value a caller can
set is a value that defeats the check.
`TestAnUnrotatedTokenIsRefusedFromOffMachine` sends `X-Forwarded-For:
127.0.0.1` from `203.0.113.7` and asserts it changes nothing.

And `middleware.GetClientIP` is correct today — the chain aliases
`ClientIPFromRemoteAddr`, which trusts only the peer — but reading it here
would make a security control depend on a middleware staying installed, in the
right order, with that aliasing unchanged. That is the kind of coupling that
fails quietly. `net.SplitHostPort(r.RemoteAddr)` cannot.

An address that will not parse is returned as it stands, which the exposure
check reads as "not this machine" — the safe answer when the transport cannot
say where a caller is.

## Refusal shape

One message for every cause, as `launchRefusal` already is, with the single
carve-out plan invariant 9 names: a `STARPORT_` value gets a message saying
which credential the field wants. A wrong secret and a malformed body get the
identical string, asserted by collecting all four refusals and comparing them.

The pasted value is never logged — not even a prefix, which the launch route
does log for tickets. A ticket is spent and gone in a minute; the local admin
token is long-lived, and a log line is a place a long-lived secret should never
reach.

## Tests

`internal/server/controllers/console_session_test.go`, seven cases:

| Test | Holds |
| --- | --- |
| `TestAPastedTokenOpensASession` | 204, both cookies, and the opposite HttpOnly flags they need. |
| `TestAWrongPasteIsRefusedWithOneMessage` | Four different internal failures, one identical string. |
| `TestAGatewayAPIKeyIsToldWhichCredentialTheFieldWants` | The carve-out survives the HTTP boundary. |
| `TestARefusedPasteClearsAStaleSession` | A failed paste expires the cookies from a rotated token. |
| `TestAnUnrotatedTokenIsRefusedFromOffMachine` | The peer address decides, and a forged forwarded header does not. |
| `TestABuildWithNoLocalTokenRefusesRatherThan404s` | A nil gate refuses; 404 would be a false statement about the build. |
| `TestTheSessionExpiresWithTheToken` | The route invents no lifetime of its own, and the session carries no subject. |

## Fail-before

The route 404s against CSG3 — it does not exist. Three negative controls on
top, each restored immediately after:

1. **Ignore the peer address** (hard-code `127.0.0.1` into `callerHost`) —
   `TestAnUnrotatedTokenIsRefusedFromOffMachine` fails at the status assertion,
   and the log shows the off-machine paste being admitted.
2. **Collapse the carve-out** (`message = sessionRefusal` in the
   `ErrGatewayAPIKeyPresented` branch):

   ```
   --- FAIL: TestAGatewayAPIKeyIsToldWhichCredentialTheFieldWants
       Error: Should not be: "That value did not open a console session. …"
   ```

3. **Stop clearing cookies on refusal** —
   `TestARefusedPasteClearsAStaleSession` fails: `Should be true`.

## Checks

| Check | Result |
| --- | --- |
| `bash scripts/verify-console-session-grants.sh` | `Summary: 11 passed, 5 failed` — matches the CSG4 target |
| `go test ./... ` | all packages ok |
| `go test ./internal/server/... -race` | ok |
| `go vet ./...` | clean |
| `make lint` | 0 issues |
| `bash scripts/verify-auth-onboarding.sh` | 26 passed, 0 failed |
| `bash scripts/verify-package-layout.sh` | passed |
| `bash scripts/verify-dependency-direction.sh` | 6 passed, 0 failed |
| `bash scripts/verify-v1-architecture.sh` | 12 passed, 0 failed |
