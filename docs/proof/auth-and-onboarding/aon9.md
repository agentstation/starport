# AON9 Launch tickets and console sessions

Date: 2026-08-25
Plan: [Auth and Onboarding](../../plans/auth-and-onboarding-plan.html)
Verifier: `scripts/verify-auth-onboarding.sh`, AON-V25

## What changed

AON8 gave the machine a credential it could hand its operator. AON9 spends it.

```text
starport ui              mint a launch link and open it
starport auth url        the same link, printed, with --open and --copy
GET /launch?lt=<ticket>  spend the link, receive a session
starport dev             does all of it for you
```

An operator who installs Starport now types one command and gets a working
console. Nothing is pasted into a browser, and no gateway API key exists yet.

## The fifth credential

The campaign has been separating four things. This adds a fifth, and it is the
first one a new operator meets.

| | What it proves | Where it lives | Ends when |
| --- | --- | --- | --- |
| Gateway API key | who you are | encrypted storage | the key is deleted |
| Environment credential | the operator's shell | the process environment | the process ends |
| Gateway credential | the operator applied it | storage, scope `*` | the operator removes it |
| BYOK | a tenant brought it | storage, scope `tenant:<id>` | the tenant removes it |
| **Console session** | **this browser was opened from that machine** | **an HttpOnly cookie** | **12 hours, or a rotation** |

A session names a machine, not an account. That is the whole reason it can be
handed out without an admin act: the thing it proves is something the operator
already had, and could have used a different way.

## Why the ticket is stateless

`MintTicket` runs in the CLI. There is no ticket endpoint, and the gateway is
never asked for one.

The reason is the case that matters most. An operator reaches for `starport ui`
when the console will not let them in — and a command that had to authenticate
to the gateway to produce a sign-in link would fail exactly then. Reading the
token file works whether the gateway is up, down, wedged, or has never run.
`TestUIWorksBeforeTheGatewayHasEverRun` holds that: the command mints the token
file itself if the machine is cold.

```go
signingKey(token, purpose) = HMAC(token.Secret, purpose)
sign(token, purpose, payload) = base64url(payload) + "." + base64url(HMAC(key, payload))
```

Two purposes, `starport.launch-ticket.v1` and `starport.console-session.v1`,
derive two keys from one secret. A 90-second ticket therefore cannot be
presented as a 12-hour session, and neither can be presented as the other after
a future third purpose is added.

Deriving rather than using the secret directly is what makes rotation a
revocation. `starport auth rotate` writes a new secret, every derived key
changes, and every outstanding ticket and every live session stops verifying —
with no session list to walk and nothing to clear.
`TestRotatingTheLocalTokenEndsALiveSession` asserts the browser half of that,
`TestALaunchLinkFromAnotherTokenOpensNothing` the link half. A forged signature
and a rotated one produce the same `ErrBadSignature`, because they are the same
answer: this was not signed by the secret I hold.

## The one thing that cannot be stateless

Single use. A signature verifies as many times as it is presented, so the
gateway remembers spent nonces — and only until their own expiry, which is 90
seconds. The set is bounded by the mint rate over a minute and a half, and it
forgets on its own.

The property is worth the state. A launch URL survives in shell history, in a
clipboard, in a terminal that was screen-shared, and in whatever the operator
pasted it into. The second use is the one an attacker gets.
`TestALaunchLinkOpensASessionOnce` spends a ticket twice and asserts 303 then
401.

Ninety seconds is the window between a person typing a command and a browser
opening. It is not a window in which a link travels somewhere and is used.

## Two cookies

```go
SessionCookie       = "starport_session"          // HttpOnly, SameSite=Lax
SessionMarkerCookie = "starport_session_present"  // readable, value "1"
```

The credential is HttpOnly, so the console's JavaScript cannot read it, cannot
log it, and cannot forward it. But the console still has to know whether it is
signed in, or it would render a key prompt to an operator who does not need one.
The marker answers that question and carries no authority: forging it gets a
browser a console that renders and a gateway that refuses every call.

`SameSite=Lax` lets the cookie ride the redirect from `/launch` — which is a
cross-document navigation the operator initiated — while keeping it off
cross-site requests another page makes.

The `Secure` attribute follows the request rather than a setting, because the
common deployment is `http://127.0.0.1` and a `Secure` cookie there is a cookie
the browser drops.

## Bearer beats cookie

```go
apiKey := extractAPIKey(r)
if apiKey == "" {
    // …session…
}
```

The session is read only when no key was presented. An `Authorization` header is
something a caller chose to send; a cookie is something the browser attached on
its own. When both arrive, the deliberate one decides — otherwise a console
session left open in a tab would silently override a key a client set, and the
client would be authenticated as somebody else with no way to tell.
`TestAnExplicitKeyBeatsAnAmbientSession` sends both and asserts the bad key is
refused rather than rescued by the good cookie.

A session holder is `identity.LocalOperator()`: key ID `local-operator`, scope
`*`, tenant `default`. It gets the wildcard because it already had it — the
holder can read the token file, rotate the secret, and edit the configuration.
Narrowing the console below what a shell on the same machine can do would be
theatre.

`requestctx.WithAPIKey` is deliberately **not** called. Nothing downstream may
believe a bearer key was presented, because a reader that found one there would
be holding a credential it could forward.
`TestASessionAuthenticatesWithNoBearerKey` asserts its absence.

## Two refusals, deliberately different

| What arrived | Status | Body |
| --- | --- | --- |
| no credential at all | 401 | `Missing API key` |
| a cookie that will not verify | 401 | This console session is no longer valid. Run `starport ui` to open a new one |

A caller with no credential has not signed in. A caller with a stale cookie has
one to replace, and only they should be told which command replaces it. The
distinction is `errNoSession` versus everything else, and
`TestAnUnusableSessionSaysHowToOpenANewOne` holds both halves.

The `/launch` refusal does the same thing and one more: it clears both cookies.
A refused launch that left the marker behind would give the console a browser
claiming to be signed in and a gateway refusing every call it made.
`TestARefusalClearsAStaleSession` asserts both names appear with a negative
`Max-Age`.

## What is never written down

`TestARefusalNamesTheWayBackInAndNothingElse` asserts the refusal page does not
contain the ticket that was presented — a credential-shaped value echoed into a
page is a credential in the browser's cache.
`TestNoCommandPrintsTheLocalTokenWhileHandingOutALink` runs `ui` and `auth url`
and asserts the token secret appears in neither output. Logs carry
`ticket_prefix`, the first 8 characters, which is enough to correlate two log
lines about one link and not enough to spend it.

The gateway API key never appears in a response body, a redirect URL, or a log
line, because AON9 never handles one. The sign-in path does not touch the key
plane at all.

## A nil gate refuses everything

A gateway assembled without a local admin token has a nil `*localauth.Gate`.
Every method on it is nil-safe and answers no: `/launch` refuses,
`TestAGatewayWithNoLocalTokenRefusesEveryLaunch`; session cookies are ignored,
`TestAGatewayWithNoGateIgnoresSessionCookies`. Bearer keys are unaffected. That
is the right answer rather than a panic, because the composition that produces
it is a legitimate one — a deployment that authenticates with keys alone.

## The browser is opened beside the gateway, not before it

`starport dev` prints its banner, then starts listening. A browser opened
between those two moments shows a connection error, and the ticket is spent.

```go
go openConsoleWhenReady(ctx, deps, session)
```

The goroutine TCP-dials the gateway's address every 25ms until it answers or 15
seconds pass, then opens the link. It writes nothing to the terminal: the link
is already in the banner, and a second thread printing into a starting gateway's
output is a race for no gain.
`TestDevelopmentOpensTheConsoleOnceItIsListening` runs it against a real
listener.

The link is printed either way. `writeLaunch` prints before it opens or copies,
so an operator whose browser did not come up still has the URL.

## Three ways to be nobody

```go
func browserSuppressed(deps Dependencies, writer io.Writer) string {
	if os.Getenv("CI") != "" { return "CI is set" }
	if os.Getenv("NO_BROWSER") != "" { return "NO_BROWSER is set" }
	if !terminalCheck(deps)(writer) { return "output is not a terminal" }
	return ""
}
```

The third rule is the one that earns its place. `starport ui > link.txt` and
every wrapper script that captures the output set neither variable, and a
browser opened for them burns a 90-second ticket where nobody is watching.
`TestARedirectedRunIsNotATerminal` states it against the real check.

Which is also why the check is injectable. `Dependencies.Desktop` groups the
three things that reach the operator's machine — the browser, the clipboard, and
whether anyone is watching — and a nil field selects the real implementation. A
test that wants to observe an open has to say it is a terminal, because
otherwise it is asserting against a rule that is correct. Bolting the terminal
question onto `writerIsTerminal` alone would have made the open path unreachable
in tests, and an untested open path is the one that breaks.

Suppression still prints. `TestUISuppressesTheBrowserWhereThereIsNobodyToSeeIt`
asserts the link and the reason both appear, because an operator on a machine
reached over SSH needs the URL and a command that opened nothing and said
nothing leaves them with nothing.

A browser that fails to start is not a failed command, either. The operator is
holding the link, which is what they asked for.

`ClipboardWriter` takes a context, and the gateway wait dials through a
`net.Dialer` rather than `net.DialTimeout`. Both were lint findings and both
were real: an operator who interrupts `starport auth url --copy` should not wait
out a wedged `xclip`, and one who interrupts a starting `starport dev` should
not wait out a dial that has stopped mattering.

## The welcome prints once

`Paths.WelcomeStampFile` is a file under the data directory. If it exists, the
greeting is skipped.

```text
Welcome to Starport.

  starport ui        Open the console. No key to paste: the link signs
                     this browser in with a session from this machine.
  starport auth url  Print that link instead of opening it.

A gateway API key is for a program calling the gateway, not for the
console. Create one in the console under Keys, or with `starport init`.
```

It answers the two questions a first run leaves open — how to reach the console,
and what the credential in front of them is for. Both are only true on a first
run. An operator who has started the gateway before is not a new operator, and a
banner that repeats every start is one an experienced reader learns to scroll
past on the day they needed it.

The last paragraph is the vocabulary the whole campaign exists to fix, in the
first thing a new operator reads. `TestTheWelcomeSeparatesTheTwoCredentials`
holds it there.

Every failure in `greetOnce` is swallowed. A gateway that refused to start
because a cosmetic stamp file could not be written would be trading a real
service for a nicety, and the worst a lost stamp can do is greet twice.

## The console stops asking for a key it does not need

`console/src/lib/api.ts` now resolves a credential rather than reading a key:

```ts
type Credential = { kind: "session" } | { kind: "key"; value: string } | { kind: "none" }
```

A session sends no header at all — the browser attaches its cookie, and this
code cannot read it. `useApiKey` became `useGatewayAccess` throughout, because
the question every consumer was actually asking was whether this browser can
reach the gateway, and only one of the two answers is a key.

Settings tells a session holder what they are holding, and names the command
that ends it:

> This browser holds a console session from `starport ui`. It signs requests on
> its own, so no key below is needed or sent. Run `starport auth rotate` on the
> gateway machine to end every session at once.

Two console tests that mocked `getApiKey` were changed to mock `hasCredential`.
The mock had been reaching past the thing under test to the storage read
underneath it; what those tests need is a browser that can reach the gateway,
and that is now something they can say directly.

## Fail-before

On the AON8 head (`c187c5a`):

```text
$ git grep -c '/launch' c187c5a -- internal/server/routes.go
exit=1

$ curl -sS -o /dev/null -w '%{http_code}' localhost:8080/launch?lt=x
404
```

## Tests

| Test | What it holds |
| --- | --- |
| `TestATicketWorksOnce` | the property the spent set exists for |
| `TestConcurrentRedemptionsSpendATicketOnce` | two browsers racing the same link |
| `TestAnExpiredTicketIsRefused` | 90 seconds, asserted past the boundary |
| `TestSpentTicketsAreForgottenOnceTheyExpire` | the set does not grow without bound |
| `TestRotatingTheTokenRefusesOutstandingTickets` | revocation with no list to clear |
| `TestATicketIsNotASession` | purpose separation, both directions |
| `TestTamperedValuesAreRefused` | payload and signature, edited apart |
| `TestAnExpiredSessionIsRefused` | 12 hours, asserted past the boundary |
| `TestRotatingTheTokenInvalidatesEverySession` | one rotation ends every browser |
| `TestSessionCookiesKeepTheSecretAwayFromScripts` | HttpOnly on the credential, not the marker |
| `TestClearedSessionCookiesExpireBoth` | no browser is left half signed in |
| `TestTicketPrefixIsTooShortToReplay` | what reaches a log cannot be spent |
| `TestLaunchURLCarriesTheTicketAndNothingElse` | no secret rides the query string |
| `TestBrowsableBaseNamesAnAddressAPersonCanOpen` | `0.0.0.0` is not a place to point a browser |
| `TestAZeroGateRefusesRatherThanPanics` | every method is nil-safe |
| `TestGateMintsTicketsItsOwnRedeemAccepts` | the two halves agree |
| `TestALaunchLinkOpensASessionOnce` | 303 then 401, one ticket |
| `TestAnExpiredLaunchLinkOpensNothing` | expiry is enforced at redemption |
| `TestALaunchLinkFromAnotherTokenOpensNothing` | rotation is revocation |
| `TestARefusalClearsAStaleSession` | both cookies, negative `Max-Age` |
| `TestARefusalNamesTheWayBackInAndNothingElse` | `starport ui`, no ticket echo, `no-store` |
| `TestAGatewayWithNoLocalTokenRefusesEveryLaunch` | a nil gate refuses rather than panics |
| `TestASessionAuthenticatesWithNoBearerKey` | scope, tenant, and no key in the context |
| `TestRotatingTheLocalTokenEndsALiveSession` | the operator's revocation story |
| `TestAnExpiredSessionStopsAuthenticating` | 12 hours, asserted past the boundary |
| `TestAGatewayWithNoGateIgnoresSessionCookies` | bearer-only deployments are unaffected |
| `TestAnUnusableSessionSaysHowToOpenANewOne` | the two refusals stay different |
| `TestAnExplicitKeyBeatsAnAmbientSession` | a cookie never overrides a header |
| `TestUIPrintsALinkTheGatewayWillAccept` | the CLI's link redeems against the gateway's gate |
| `TestUIWorksBeforeTheGatewayHasEverRun` | the case an operator needs it most |
| `TestEachLaunchLinkIsDifferent` | two runs are not two copies of one ticket |
| `TestUIOpensTheLinkItPrinted` | the opened URL is the printed one |
| `TestUISuppressesTheBrowserWhereThereIsNobodyToSeeIt` | `CI` and `NO_BROWSER`, link still printed |
| `TestARedirectedRunIsNotATerminal` | the third rule, against the real check |
| `TestAuthURLCopiesOnRequestAndNotOtherwise` | nothing reaches the clipboard uninvited |
| `TestDevelopmentOpensTheConsoleOnceItIsListening` | the first-run path, against a real listener |
| `TestDevelopmentOpensNoBrowserInAutomation` | the acceptance criterion verbatim |
| `TestDevelopmentHonoursNoOpen` | the link without the browser |
| `TestTheWelcomePrintsOnce` | the greeting does not become noise |
| `TestTheWelcomeSeparatesTheTwoCredentials` | the vocabulary, in the first thing read |
| `TestNoCommandPrintsTheLocalTokenWhileHandingOutALink` | the links are safe; the secret is not |

## Repository gates

```bash
go test ./internal/localauth/ ./internal/server/... ./internal/cli/ ./internal/app/  # ok
go test ./...                                                                        # ok
bash scripts/verify-auth-onboarding.sh                                               # 25 passed, 1 failed
cd console && npx tsc --noEmit && npm test                                           # 12 files, 77 tests
```

The remaining verifier failure is AON-V26, the provider screens, which AON10
owns.

## What AON9 deliberately did not do

**No session list, and no logout route.** Revocation is `starport auth rotate`,
which ends every session at once. A per-session revocation surface would need a
store, a session identifier, and a console screen listing browsers — for a
credential that already expires in 12 hours on a machine the operator is sitting
at.

**No refresh.** A session lasts 12 hours and then the operator runs
`starport ui` again. Sliding expiry would keep a browser signed in indefinitely
as long as it was polling, which is the opposite of what a 12-hour bound is for.

**No remote sign-in.** The launch link only proves the holder could read a file
on the gateway's machine. That is exactly the right claim for a local console
and exactly the wrong one for a hosted deployment, which will need a real
identity provider rather than a longer-lived version of this.
