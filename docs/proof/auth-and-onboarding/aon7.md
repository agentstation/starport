# AON7 Console authentication switch

Date: 2026-08-23
Plan: [Auth and Onboarding](../../plans/auth-and-onboarding-plan.html)
Verifier: `scripts/verify-auth-onboarding.sh`, AON-V22

## What changed

AON6 made the authentication mode an operator's to state, but only before the
process started. An operator who wanted to open or close the gateway had to edit
configuration and restart it. AON7 makes the mode a running value the console
can change, and makes a change survive the restart it no longer requires.

```text
GET  /api/v1/auth/mode         unauthenticated, both modes
PUT  /api/v1/admin/auth/mode   admin scope, loopback only
```

The console renders it under Settings, directly beneath Connection: the key
above is what this browser presents, and the switch below is whether the gateway
asks anyone for one.

## The vocabulary moved to one owner

AON6 spelled the mode twice — `config.AuthMode` for the operator and a resolved
`string` at the HTTP seam — and guarded the pair with
`TestAuthModeVocabularyMatchesAcrossSeams`.

AON7 cannot keep that shape. The switch has to apply the AON6 exposure tripwire
at runtime, and that rule reads the mode, the bind host, and the acknowledgment
together. Restating it at the HTTP seam would give one rule two versions, and
the version that drifts open is an open gateway.

So `internal/authmode` owns all of it:

| Concept | What it is |
| --- | --- |
| `Mode`, `Effective`, `Valid` | The two modes, and what an unset one means |
| `Source` | Which of default, config, flag, or console stated the current mode |
| `Setting`, `Resolve` | One decision, and the precedence that produces it |
| `AllowsDisabled` | The exposure tripwire, called by startup and by the switch |
| `LoopbackHost`, `LoopbackAddr`, `LoopbackOrigin` | What counts as this machine |
| `Policy` | The running mode, read per request |
| `Repository` | The stored mode, versioned and CAS-guarded |

`config.AuthMode` is now an alias of `authmode.Mode`, and `server.Config.AuthMode`
is the same type. The drift test was deleted rather than updated, with the
reason recorded in `internal/app/auth_mode_test.go`: there are no longer two
spellings to compare.

## Precedence

`Resolve(stated, source, persisted)` decides in one place:

| Stated for this process | Stored | Result |
| --- | --- | --- |
| nothing | nothing | `required` from `default` |
| nothing | `disabled` from console | `disabled` from `console` |
| config or flag | anything | the stated mode, stored value ignored |

Configuration and flags win. A stored value that overrode them would turn a
deployment's own statement into a suggestion, and the inverse matters more: a
stored `required` beating `--no-auth` would strand an operator outside a gateway
they cannot log in to change.

An empty mode from a stated source is still a statement — it resolves to
`required` from that source, not to the stored value.

## A stored mode is re-validated against this process

The exposure tripwire reads the bind address, and the bind address belongs to
the process, not to the record. A data directory carried from a laptop to a
public address would otherwise carry an open gateway with it.

`runtimeBuilder.resolveAuthMode` re-runs `AllowsDisabled` over a stored
`disabled` and falls back to `required`, with a warning naming the bind host. A
*stated* disabled mode is left alone: startup validation already refused the
unsafe combination, and repairing it here would swallow an operator's explicit
choice instead of reporting it.

## The switch is not only the admin scope

The first working implementation guarded `PUT /api/v1/admin/auth/mode` with
`requireAdmin` alone. `TestSwitchChangesInferenceWithoutARestart` failed on the
second half: re-enabling returned 403.

The cause is the AON6 rule that disabled means disabled. A gateway with
authentication off resolves every request to the anonymous identity, which holds
no `admin` scope on purpose. So there is no key that can carry admin, and the
scope guard made the switch a one-way door — an operator could open the gateway
from the console and never close it again without editing configuration and
restarting.

`Server.requireSwitchAccess` is the fix:

```go
guarded := s.requireAdmin(next)
if s.authPolicy.Disabled() {
    next.ServeHTTP(w, r)   // no key exists to hold admin
    return
}
guarded.ServeHTTP(w, r)    // ordinary admin plane
```

What remains in the open state is the controller's own guard, which is stricter
than the admin scope in the direction that matters: the request has to come from
the machine running the gateway. An open gateway can be locked from that
machine, and cannot be opened further from anywhere else.

The `/admin` group was restructured so the switch sits outside `r.Use(requireAdmin)`
and carries `r.With(s.requireSwitchAccess)`; every other admin route is
unchanged inside an inner `r.Group`.

## One refusal, read and write

`AuthController.refusal` answers a single question — may *this* request change
the mode — and both handlers call it. `GET` renders it as `can_change` and
`reason`; `PUT` returns it as 403.

```json
{"mode":"required","source":"flag","can_change":false,
 "reason":"the authentication mode is fixed by a command line flag for this process"}
```

Two independent judgments would let the console render an available control the
switch would then reject. One function cannot.

The four refusals, in the order they are checked:

| Condition | Reason |
| --- | --- |
| no policy or no store | a change would not survive a restart |
| source is `flag` | fixed by a command line flag for this process |
| source is `config` | fixed by `STARPORT_SECURITY_AUTH_MODE` |
| remote address or `Origin` is not this machine | can only be changed from this machine |

Each names the thing an operator would edit. A refusal that only says no leaves
them guessing which of three places set the mode.

An absent `Origin` passes: curl and every SDK send none, and refusing those
would make the header a requirement rather than a check. `null` fails, because
it names no host and so cannot be shown to be this machine, and a hostname like
`localhost.attacker.example` fails because the check is textual — deciding it by
DNS would trust a resolver with a security question and accept an answer that
can change after startup.

## Storage

`authmode.Repository` is a one-record store at `authmode:v1:current`, with a
revision the writer CASes against. The controller reads the current revision and
writes against it, so two consoles racing produce one winner and one 409 rather
than a lost write.

A stored mode the binary does not recognize is `ErrCorruptRecord`, not a silent
fallback. `Put` refuses an invalid mode with `ErrInvalidMode`, which is what
turns an unknown mode into a 400 at the HTTP seam rather than a 500.

The policy is only updated after the write succeeds. A running gateway that
opened and then failed to record it would close again on restart with nothing
saying why.

## The console asks before it commits

`AuthModeControl` is a two-option radiogroup with a confirmation modal, and the
confirmation states the consequence in the reader's terms. What it says depends
on whether this browser holds a key:

```text
Every request will need a gateway API key. This browser has one saved, so the
console keeps working; anything else calling this gateway without a key starts
getting 401.

Every request will need a gateway API key, and this browser has none saved. The
console locks until you save a key under Connection above. Create one on the
Keys screen first, or run starport init in a terminal.
```

Locking a gateway from a browser that holds no key locks this console out of it.
A reader has to learn that before the switch, not from the next screen full of
401s.

On success the control writes the response into the cache and invalidates
everything else: the mode decides what every other request needs, so everything
the console had already read was read under the old one.

When `can_change` is false the control renders disabled with the gateway's own
`reason` as its caption.

## Fail-before

The plan's stated condition is that the endpoint returns 404 on the AON6 head. A
trimmed switch test — an admin key, a loopback caller, `PUT /api/v1/admin/auth/mode` —
run in a clean worktree at `d3fb6d8`:

```text
--- FAIL: TestAON7FailBefore (1.88s)
    Error:      Not equal:
                expected: 200
                actual  : 404
    Messages:   AON7 expects the switch to exist
```

## Tests

Twenty-five new tests across four files, plus repairs to the AON6 suite.

| Test | Package | What it states |
| --- | --- | --- |
| `TestSwitchChangesInferenceWithoutARestart` | `internal/server` | A keyless inference request goes 401 to 200 and back, with no restart between |
| `TestSwitchClosesAnOpenGatewayWithoutAKey` | `internal/server` | An open gateway can be locked from this machine with no key, and cannot be reopened by a remote caller or by an anonymous one once locked |
| `TestSwitchSurvivesARestart` | `internal/server` | A second server built over the same store, through the real `Resolve`, serves the stored mode |
| `TestSwitchRefusesANonAdminCaller` | `internal/server` | A `chat:write` key gets 403 and the gateway stays closed |
| `TestSwitchRefusesARemoteOrigin` | `internal/server` | An admin key from another site's page, and an admin key from the network, both get 403 |
| `TestSwitchRefusesToOpenAReachableGateway` | `internal/server` | `0.0.0.0` plus `disabled` is 409; locking the same gateway is always allowed |
| `TestSwitchRefusesAModeStatedForThisProcess` | `internal/server` | Flag and config sources get 403, and the body names the flag or the variable |
| `TestSwitchRejectsAnUnknownMode` | `internal/server` | `""`, `off`, and `REQUIRED` are 400, not stored |
| `TestModeReadAnswersTheConsoleQuestion` | `internal/server` | The read carries mode, `can_change`, and a reason, and refuses the remote caller the write would refuse |
| `TestResolvePrecedence` | `internal/authmode` | Five cases fixing config and flag above a stored mode, and an empty stated mode as a statement |
| `TestAllowsDisabled` | `internal/authmode` | The tripwire at its one owner, including the empty host and the acknowledgment |
| `TestLoopbackHost` | `internal/authmode` | Twelve cases, including `localhost.attacker.example` |
| `TestLoopbackAddr` | `internal/authmode` | Host-port and bare forms, IPv4 and IPv6 |
| `TestLoopbackOrigin` | `internal/authmode` | Absent passes, `null` and another site fail |
| `TestPolicyIsReadPerRequest` | `internal/authmode` | A captured mode would make `disabled` mean "disabled at boot" |
| `TestPolicyFailsClosed` | `internal/authmode` | A nil policy and a zero policy both require a key |
| `TestModeValid` | `internal/authmode` | The mode is compared exactly, not case-folded |
| `TestRepositoryContract` | `internal/authmode` | Not found, first write at revision 1, read-back, one key under the prefix, update, stale revision conflict, invalid mode |
| `TestCreateRefusesAnExistingRecord` | `internal/authmode` | Revision 0 is create, not blind overwrite |
| `TestGetRefusesAnUnrecognizedStoredMode` | `internal/authmode` | A mode this binary cannot read is an error, not a default |
| `TestOpenRequiresAStore` | `internal/authmode` | A missing store is refused at composition, not at the first write |
| `TestServerConfigCarriesTheResolvedDecision` | `internal/app` | Four cases carrying the resolved mode, source, and store to the HTTP seam |
| `TestAuthModeSourceNamesWhatStatedIt` | `internal/app` | Unset, config, and the `--no-auth` override each name their source |
| `TestRequireIdentityJudgesTheResolvedMode` | `internal/app` | A console-disabled gateway starts with no keys; `required` still refuses |
| `AuthModeControl.test.tsx` | `console` | Five tests: the confirmation appears before the switch commits, cancelling changes nothing, the lockout is named when this browser holds no key, a stored key changes the promise, a mode fixed by the operator renders locked with the gateway's reason |

`internal/server/auth_mode_test.go` was repaired to the new types, and its
`can_change` case gained an assertion that the reason is non-empty: `httptest`
gives a request a non-loopback remote address, which is exactly the case AON7
refuses, and the read still answers.

## Repository gates

```text
go build ./...                                     clean
go vet ./...                                       clean
go test ./...                                      no failures
go test -race ./internal/authmode/... ./internal/server/... ./internal/app/... ./internal/config/...  no failures
make lint                                          0 issues
make build                                         Build complete: ./starport
console pnpm typecheck                             clean
console pnpm test                                  12 files, 77 tests passed
bash scripts/verify-starmap-ownership.sh           Summary: 12 passed, 0 failed
bash scripts/verify-v1-architecture.sh             Summary: 12 passed, 0 failed
bash scripts/test-dependency-direction-verifier.sh passed
bash scripts/verify-dependency-direction.sh        Summary: 6 passed, 0 failed
bash scripts/verify-catalog-driven-providers.sh    Summary: 19 passed, 0 failed
bash scripts/verify-package-layout.sh              passed
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
bash scripts/benchmark-overhead.sh                 PASS
bash scripts/smoke-openrouter-sdks.sh              PASS Python, TypeScript, Go
bash scripts/verify-auth-onboarding.sh             Summary: 22 passed, 4 failed
```

The four remaining conditions are AON-V23 through AON-V26, owned by AON8, AON9,
AON10, and AON13.

## What AON7 deliberately did not do

- No local admin token. AON8 owns `internal/localauth`; the switch's loopback
  guard is a check on where a request came from, not a credential.
- No per-tenant or per-route mode. The switch is deployment-wide, the same
  scope AON6 gave it.
- No CORS change. The `Origin` check refuses a cross-site write on this one
  route; it does not make the gateway browser-reachable from anywhere new.
- No console screen of its own. The switch is a Settings control, because it
  belongs beside the key this browser presents.
