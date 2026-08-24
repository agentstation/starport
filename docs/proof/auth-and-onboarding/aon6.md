# AON6 Authentication mode

Date: 2026-08-23
Plan: [Auth and Onboarding](../../plans/auth-and-onboarding-plan.html)
Verifier: `scripts/verify-auth-onboarding.sh`, AON-V19, AON-V20 and AON-V21

## What changed

`routes.go` applied `requireAPIKey` to every route that mattered, so the first
thing a new user met was a 401 and the first thing an operator behind their own
front door met was a key they did not want to manage. AON6 makes the decision
an operator's to state:

```bash
starport dev --no-auth
starport serve --no-auth --allow-remote-no-auth
STARPORT_SECURITY_AUTH_MODE=disabled
```

The default is unchanged. An unset mode is `required`, because the state an
operator reaches by not deciding has to be the safe one.

| Mode | Inference without a key | Admin plane without a key | `GET /api/v1/auth/mode` |
| --- | --- | --- | --- |
| `required` (default) | 401 | 401 | 200 |
| `disabled` | 200 as `anonymous` under `default` | 403 | 200 |
| `disabled` with `admin` named | 200 | 200 | 200 |

## Disabled turns the check off; it does not make it optional

A request that presents a valid gateway API key while the mode is `disabled`
still runs as the anonymous identity under the `default` tenant. The key is
read by nothing.

The alternative — honor a key when one arrives, fall back to anonymous when it
does not — makes the caller's identity depend on a header they may not know
they sent. A stale key from a shell history, a mistyped secret, or a
copy-pasted curl would silently move a caller onto another account's limits,
budgets, and BYOK credentials, and nothing in the response would say so. One
mode, one identity. `TestDisabledAuthenticationIgnoresAPresentedKey` pins it.

## The anonymous identity is a whole identity

Everything behind authentication reads the request context: rate limiting
refuses to run without an API key ID, budgets key off it, and usage records
attribute to it. An unauthenticated request that carried none would meet those
seams in a state they never see in production.

So `internal/identity/anonymous.go` names one:

```go
const AnonymousKeyID = "anonymous"
func Anonymous(scopes []string) APIKey
func DefaultAnonymousScopes() []string
```

It is not a key. Nothing issues it, nothing stores it, and no hash matches it.
It names no account, so it resolves to the canonical tenant through the same
`EffectiveTenantID` rule every issued key uses. Downstream seams need no
special case at all.

## The admin plane stays closed

Disabling authentication says the port is trusted, not that every caller is the
operator. The default scope set is every tenant scope and never `admin`:
`chat:write`, `embeddings:write`, `models:read`, `activity:read`,
`presets:write`, `provider_keys:read`, `provider_keys:write`. Issuing keys,
applying deployment-wide gateway credentials, and deleting accounts should not
follow from opening inference.

An operator who wants the admin plane open without a key says so by name:

```bash
STARPORT_SECURITY_UNAUTHENTICATED_SCOPES=admin
```

`identity.APIKey.HasScope` treats only `"*"` as a wildcard, so `admin` has to be
listed explicitly and the two admin tests prove both directions. The set is a
policy and not a mirror of the route table: a scope added to a route and not
added here refuses an unauthenticated caller, which is the direction a default
should fail in.

## The exposure tripwire

An unauthenticated gateway on a reachable address is an open inference
endpoint, and the bind address is the only evidence startup has about who can
reach it. `Config.validateAuthenticationExposure` refuses that combination
unless a second explicit acknowledgment is present:

```text
authentication is disabled and the server binds "0.0.0.0", which is not a
loopback address; pass --allow-remote-no-auth (or set
STARPORT_SECURITY_ALLOW_REMOTE_NO_AUTH=true) to serve an unauthenticated
gateway to the network
```

Two deliberate acts, and the refusal names the way out. An empty host binds
every interface, so it is the exposure the tripwire exists to catch, not a
missing answer.

The check lives on `Config` and not on `SecurityConfig`, because it is the one
decision no single section can make: it reads the mode against the bind
address.

## Why `config.Override` exists

A `--no-auth` flag that mutated the loaded config after validation would leave
the tripwire guarding the environment variable only — the path an operator is
least likely to take when opening a gateway for a quick test. Overrides apply
inside `Loader.load`, after the environment and the development contract and
before validation:

```go
func DisableAuthentication() Override
func AllowRemoteWithoutAuthentication() Override
```

An explicit flag beats both other sources, and it still meets exactly the
validation an environment value does.
`TestDisableAuthenticationOverrideMeetsValidation` pins that.

## The mode crosses one seam as a string

`internal/server` does not import `internal/config`, and AON6 does not change
that. `config.AuthMode` is the operator's vocabulary; `server.Config.AuthMode`
is a resolved `string`; `internal/app` owns the one mapping between them and
resolves the unset case, so the HTTP seam never has to decide what an empty
value means.

Two spellings of one decision can drift, and this drift fails in the worse
direction: a mode the server does not recognize is not "disabled", but a mode
it recognizes when it should not is an open gateway.
`TestAuthModeVocabularyMatchesAcrossSeams` pins the constant sets equal.

## Startup consequences

- `app.requireIdentity` refused to start with zero identities. That check is
  correct only when the mode is `required`, and it now takes the mode.
- `dev` mints no key when authentication is disabled. There is nothing to print
  and nothing to paste, and minting one anyway would teach the wrong thing
  about the mode it is running in.
- `DevelopmentSession.validate` enforces the equivalence rather than two
  independent checks: a session that requires a key must carry one, and a
  session that requires none must not have minted one.
- The `serve` and `dev` banners carry `auth_mode`. An operator who sees no key
  printed otherwise cannot tell a disabled gateway from a broken one.

```text
Starport development gateway
URL: http://127.0.0.1:8080
Authentication: disabled (no gateway API key required)
```

## `GET /api/v1/auth/mode`

Unauthenticated, in both modes, because a client that holds no key needs to
learn whether it has to go get one and answering that with 401 tells it nothing
it can act on.

```json
{"mode": "required", "can_change": false}
```

`can_change` is false throughout AON6. AON7 owns the console switch and the
admin write that makes it true.

## Fail-before

The four new test files compiled against `abd1a9c` (the AON5 merge, this
branch's base) in a clean worktree:

```text
vet: internal/config/auth_mode_test.go:14:18: undefined: AuthModeRequired
vet: internal/cli/auth_mode_test.go:19:8: undefined: GatewayOptions
vet: internal/server/auth_mode_test.go:25:26: undefined: AuthModeDisabled
vet: internal/app/auth_mode_test.go:21:25: undefined: server.AuthModeRequired
```

Each package fails on the option AON6 introduces, which is the plan's stated
fail-before condition.

## Tests

Eighteen new tests, one rewritten.

| Test | Package | What it states |
| --- | --- | --- |
| `TestDisabledAuthenticationServesInferenceWithoutAKey` | `internal/server` | A keyless `POST /v1/chat/completions` returns 200 with an empty identity store |
| `TestDisabledAuthenticationMetersTheAnonymousIdentity` | `internal/server` | The request context carries `anonymous` and the `default` tenant, which is what rate limits, budgets, and usage read |
| `TestDisabledAuthenticationIgnoresAPresentedKey` | `internal/server` | A valid key for another account still resolves to `anonymous` under `default` |
| `TestDisabledAuthenticationKeepsTheAdminPlaneClosed` | `internal/server` | `GET /api/v1/admin/keys` without a key is 403 |
| `TestDisabledAuthenticationGrantsAdminOnlyWhenNamed` | `internal/server` | The same route is 200 once the operator lists `admin` |
| `TestRequiredAuthenticationRefusesAKeylessRequest` | `internal/server` | The default build 401s the same inference request |
| `TestAuthModeRouteAnswersWithoutAKey` | `internal/server` | The route answers 200 with the running mode in both modes, `can_change` false |
| `TestAnonymousScopesExcludeAdmin` | `internal/server` | The default set holds neither `admin` nor `*`, proving the policy rather than the current route table |
| `TestAuthModeDefaultsToRequired` | `internal/config` | The zero value resolves to `required` |
| `TestSecurityConfigRejectsAnUnknownAuthMode` | `internal/config` | `optional` is a startup error, not a silent fallback |
| `TestAuthenticationExposureTripwire` | `internal/config` | Eight cases: loopback by IPv4, name, and IPv6 pass; `0.0.0.0`, a routable address, and an empty host refuse; the acknowledgment passes; `required` on a reachable address is untouched |
| `TestDisableAuthenticationOverrideMeetsValidation` | `internal/config` | The flag path meets the tripwire and the acknowledgment clears it |
| `TestAuthModeVocabularyMatchesAcrossSeams` | `internal/app` | The config and server constant sets are equal |
| `TestServerConfigCarriesTheAuthenticationDecision` | `internal/app` | Composition resolves unset, required, and disabled, and carries the scope list |
| `TestNoAuthFlagsReachTheServerRunner` | `internal/cli` | Both `serve` flags reach `GatewayOptions`; a flag that parses but never arrives is the failure that looks like success |
| `TestDevNoAuthFlagReachesTheStarter` | `internal/cli` | `dev --no-auth` reaches the starter and the banner says `Authentication: disabled` with no key line |
| `TestDevSessionRejectsAKeyModeMismatch` | `internal/cli` | Four cases pinning the equivalence between the mode and the presence of a key |
| `TestServeHelpDocumentsTheAuthenticationFlags` | `internal/cli` | `starport help serve` names both flags; a flag only the source mentions is not an option |

`TestRuntimeRequiresNamedIdentity` was rewritten to take a mode and to assert
that the disabled case starts with an empty identity store.

The acceptance clause "its usage record carries the anonymous key ID" is proven
at the request-context level rather than by reading a stored record: the test
server has no usage repository, and the context is the exact input the usage
writer consumes.

## Repository gates

```text
go build ./...                                     clean
go test ./...                                      no failures
go test -race ./internal/server/... ./internal/config/... ./internal/cli/... ./internal/app/...  no failures
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
bash scripts/verify-auth-onboarding.sh             Summary: 21 passed, 5 failed
```

AON-V22 was re-pointed from the path `auth/mode` to the symbol `Auth.SetMode`.
AON6 mounts the public read, so a path match would have reported AON7's
condition green two tasks early. What AON7 owns is the admin write.

## What AON6 deliberately did not do

- No console switch. `can_change` is hard-coded false and there is no admin
  write route; AON7 owns both.
- No per-route opt-out. The mode is deployment-wide.
- No token, session, or local admin credential. AON8 owns the local admin
  token, and `--no-auth` is not it: it opens the gateway rather than
  authenticating an operator to the console.
