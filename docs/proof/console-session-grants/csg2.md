# CSG2 — The local admin token grant

## Problem

`starport auth token` prints a value and nothing accepted it. The console had
one way in — a launch ticket — and a ticket is minted by a process on this
machine, so an operator who lost the launch line had no way back that did not
involve restarting the gateway.

## Change

`internal/localauth/grant_local_token.go` (new) registers `local-token`. It
takes a pasted claim and admits the browser when the claim is this machine's
local admin token, via the existing constant-time `Token.Authorizes`.

`NewGate` gained a `bindHost` parameter. It is the fallback origin for a grant
that judges where a caller is, used when the caller is in-process and so has no
remote address of its own. `internal/app/app.go` passes `config.Server.Host`.

Three refusals, and the split between them is the design:

| Refusal | Cause |
| --- | --- |
| `ErrTokenRejected` | Any wrong value — empty, whitespace, right prefix, one byte off. |
| `ErrGatewayAPIKeyPresented` | A `STARPORT_` value, named rather than refused. |
| `ErrRemoteTokenRefused` | An unrotated token presented from off-machine. |

## Why the exposure check lives in the grant

`internal/app/app.go:563` already refuses to start when
`AllowsExposure(config.Server.Host, token)` is false. That reads the
*configured* bind, and a reverse proxy in front of a loopback gateway satisfies
it — the process really did bind `127.0.0.1` — while making the paste route
reachable from the network. A first-boot secret has been printed to a terminal,
and a terminal is scrollback, a tmux buffer, a screen share, and a CI log.

So the grant judges the caller's address, not the process's. Rotating clears
the refusal, which is the same way out `AllowsExposure` already offers to the
bind-host case, so the two checks agree on what "safe to expose" means rather
than inventing a second rule.

## Why the gateway API key gets its own message

Plan invariant 9 says one message for every wrong secret, for the reason
`ErrBadSignature` has it: a refusal that separated "wrong secret" from "right
shape, wrong bytes" would tell a caller whether their guess was getting warmer.

A `STARPORT_` prefix is the one carve-out. It is a category error, not a guess.
The prefix is public, so naming it narrows no search space, and leaving a
reader to work out which of two credentials the field wants is exactly the
confusion this campaign exists to remove. The invariant was revised in the plan
to state the carve-out rather than have the code quietly violate it.

## Why a throttle and no lockout

The secret is 32 random bytes. No delay makes that guessable or fails to, so
the delay is not a brute-force defense — it keeps a hammering script at a few
attempts per second instead of thousands, which keeps the log readable and
stops one caller from turning the route into a way to spend the gateway's CPU.

The delay is under a mutex because a per-caller delay is one an attacker
sidesteps by opening more connections. There is deliberately no lockout: on a
single-operator gateway a lockout is a way for anyone who can reach the port to
lock the operator out of their own console, which trades a threat that does not
exist for one that does.

## Tests

`internal/localauth/grant_local_token_test.go`, five cases:

| Test | Holds |
| --- | --- |
| `TestThePastedTokenAdmitsTheOperator` | The paste opens a real session and it records `local-token`. |
| `TestAWrongValueGetsOneAnswer` | Five wrong shapes, one refusal. |
| `TestAGatewayAPIKeyIsNamedRatherThanRefused` | The carve-out, asserted as `ErrGatewayAPIKeyPresented` and explicitly *not* `ErrTokenRejected`. |
| `TestAnUnrotatedTokenIsRefusedFromOffMachine` | The reverse-proxy hole, plus both ways out: same secret from this machine, and a rotated secret from off-machine. |
| `TestFailedAttemptsAreSerialized` | Eight concurrent wrong pastes: every one pays the delay, none overlap, and a correct paste never waits. |

The last two are the ones that earn their place. The third asserts the
*negative* — a message that also matched `ErrTokenRejected` would satisfy a
naive test while telling the reader nothing. The fifth asserts the shape of the
throttle rather than its duration, so tuning the constant does not break it,
and it checks the property that makes the throttle safe to ship: an operator
who types the right value never queues behind someone else's guessing.

`TestFailedAttemptsAreSerialized` collects its refusals into a slice and
asserts after `wait.Wait()`. `require` fails by calling `runtime.Goexit`, which
off the test goroutine hangs the run instead of failing it.

## Fail-before

Two negative controls, each restored immediately after.

1. **Remove the `AllowsExposure` check from `Mint`:**

   ```
   --- FAIL: TestAnUnrotatedTokenIsRefusedFromOffMachine (0.00s)
       grant_local_token_test.go:91:
       Error: Expected error with "a local admin token is only accepted from
              this machine until it has been rotated" in chain but got nil.
   ```

2. **Remove the mutex from `throttle()`:**

   ```
   --- FAIL: TestFailedAttemptsAreSerialized (0.00s)
       grant_local_token_test.go:162:
       Error: Should be false
   ```

   (`overlap` — rejected attempts ran the delay concurrently.)

## Checks

| Check | Result |
| --- | --- |
| `bash scripts/verify-console-session-grants.sh` | `Summary: 7 passed, 9 failed` — matches the CSG2 target |
| `go test ./... ` | all packages ok |
| `go test ./internal/localauth/ -race -count=3` | ok |
| `go vet ./...` | clean |
| `make lint` | 0 issues |
| `bash scripts/verify-auth-onboarding.sh` | 26 passed, 0 failed |
| `bash scripts/verify-package-layout.sh` | passed |
| `bash scripts/verify-dependency-direction.sh` | 6 passed, 0 failed |
| `bash scripts/verify-v1-architecture.sh` | 12 passed, 0 failed |

## Call sites updated

`NewGate` gained a parameter. One production caller — `internal/app/app.go`,
which passes `config.Server.Host` — and four test files:
`internal/localauth/browser_test.go`,
`internal/server/session_middleware_test.go`,
`internal/server/controllers/launch_test.go`, and `internal/cli/ui_test.go`.
