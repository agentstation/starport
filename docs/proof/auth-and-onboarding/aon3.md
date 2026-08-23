# AON3 Three named credential sources

Date: 2026-08-23
Plan: [Auth and Onboarding](../../plans/auth-and-onboarding-plan.html)
Verifier: `scripts/verify-auth-onboarding.sh`, AON-V09 through AON-V13

## What changed

The gateway knew two provider credential planes and called the whole subsystem
BYOK. That name was wrong twice. A credential read from the process environment
is the operator's, not a tenant's, and a credential the operator applies once
for the whole deployment had no plane at all — the record layer could store one
at scope `*`, and no resolver ever asked for it. AON3 names three sources,
gives each an owner, and makes the operator's stored plane reachable.

### The vocabulary

| Term | Owner | What it is |
| --- | --- | --- |
| Gateway API key | a tenant | The `STARPORT_` bearer token. Authenticates and carries scopes. Never a credential scope. |
| Environment credential | the operator | A provider key read from the process environment. Read-only from HTTP. |
| Gateway credential | the operator | A provider key the operator applies through the console or admin API, stored at scope `*`, deployment-wide. **Not BYOK.** |
| BYOK | a tenant | A provider key a tenant brings for itself, stored at scope `tenant:<id>`. The only thing this gateway calls BYOK. |

`internal/providers/byok` becomes `internal/providers/keyring`, because the
package holds all three and only one of them is BYOK. The package doc says so:

```go
// Package keyring stores and resolves the provider credentials a request can
// spend. Three sources feed it, and two of the three belong to the operator:
// a credential read from the process environment, a credential the operator
// applies for the whole deployment at the gateway scope, and a credential a
// tenant brings for itself. Only the last of those is BYOK, which is why the
// package is not named for it.
```

`CredentialSourceOperator` and `CredentialSourceUser` become `SourceEnvironment`,
`SourceGateway`, and `SourceBYOK`. The strategies keep the name
`operator_first`, because environment and gateway are collectively the
operator's credentials:

```go
func (s Strategy) Sources() []CredentialSource {
	switch s {
	case BYOKFirst:
		return []CredentialSource{SourceBYOK, SourceEnvironment, SourceGateway}
	case BYOKOnly:
		return []CredentialSource{SourceBYOK}
	default:
		return []CredentialSource{SourceEnvironment, SourceGateway, SourceBYOK}
	}
}
```

The two operator planes stay adjacent in every order. A strategy chooses whose
money to spend first, and splitting the operator's two planes across a tenant's
would give `byok_first` a meaning no operator asked for.

### The gateway scope has one owner

`credentials.GatewayScope` holds the literal, and `keyring.GatewayScope` aliases
it, so the record layer and the resolver cannot drift:

```go
// GatewayScope is the scope of a credential the operator applies once for the
// whole deployment. Every other scope names one tenant.
const GatewayScope = "*"

// IsGateway reports whether the operator owns this credential for the whole
// deployment. A credential at any other scope belongs to one tenant, which is
// the only kind this gateway calls BYOK.
func (k ProviderKey) IsGateway() bool { return k.Scope == GatewayScope }
```

`keyring.TenantScope` builds the other side. The scope names the account and
never a key, so deleting a gateway API key cannot strand the credentials its
tenant applied. `grep -r 'user:' internal/providers/keyring` finds no scope
construction.

### One resolver serves both stored planes

`UserCredentialResolver` becomes `StoredCredentialResolver`, and the two stored
planes differ only in the scope they pass:

```go
// resolveStored reads one stored plane. The gateway plane and the BYOK plane
// differ only in their scope, so they share this path and cannot drift apart
// in how they look a provider up or how they fail.
func (p *credentialPolicy) resolveStored(
	ctx context.Context, scope string, providerID string,
) (credentials.Material, error)
```

`reachableSources` drops the planes a deployment cannot read at all — the
stored planes when no resolver is wired, the BYOK plane when the request has no
tenant. It never widens: a tenant on `byok_only` whose BYOK plane is
unreachable resolves to an empty source list and gets the existing
not-configured failure rather than falling through to an operator credential.

### Attribution stays with the payer

```go
// credentialEvidence reports who paid for the attempt. An environment
// credential and a gateway credential are both the operator's, so they record
// the same owner and differ only in where the operator put them.
```

`SourceGateway` records `CredentialOwnerOperator`. Recording it as tenant-owned
would bill the wrong party for every request the operator paid for, which is
why the mapping has its own test.

### The operator sets the ceiling, the key may narrow it

The account's `CredentialStrategy` is the governing value, and a gateway API
key may narrow it but never widen it. `keyring.EffectiveStrategy` owns that
rule:

```go
// EffectiveStrategy resolves the strategy one request actually runs under,
// from the account's governing strategy and the authenticated key's own
// metadata. A key that names no strategy inherits the account's.
func EffectiveStrategy(governing Strategy, metadata map[string]any) (Strategy, error)
```

The distinction between "inherits" and "asked for the default" is the whole
correctness of this seam. The deleted `StrategyFromMetadata` collapsed the two
into one value, so a narrowing rule written on top of it would have refused
every ordinary key under a `byok_only` account — most keys carry no strategy
metadata at all. `TestKeyWithoutStrategyInheritsTheTenantStrategy` pins it.

Narrowing is judged by which sources a strategy can reach, not by its name:
`operator_first` and `byok_first` reach the same three planes, so reordering is
permitted. Only asking for an operator plane the account withholds is refused.

`AuthMiddleware` reads the account once per request and puts the record on the
context, so the HTTP boundary owns the decision and the router consumes an
already-effective value. It holds a one-method reader rather than the
repository:

```go
// TenantReader reads one account by ID. The middleware holds this single
// method rather than the tenant repository, so the HTTP seam never learns how
// an account is stored or gains the power to write one.
type TenantReader interface {
	GetByID(ctx context.Context, id string) (tenant.Record, error)
}
```

An unreadable account never fails the request. The key authenticated, and a
storage fault on the account record has a safe default — refusing would take a
working deployment offline for a fault the default policy already covers.

### The refusal says which kind it is

A strategy the gateway cannot parse is the caller's mistake and answers 400. A
strategy it understands but the operator withheld answers 403, because the
caller is authenticated and the denial is deliberate. `writeCredentialStrategyError`
splits them in both wire dialects, and both chat and embeddings route through
it instead of the previous hardcoded 400.

## Evidence

### Fail-before

The plan predicted: a test that stores a scope `*` credential and expects a
served request fails on the baseline, because no resolver consults that plane.
Adapted to the AON2 head (`6e5b12c`) API — `WithUserCredentials`, `byok.OperatorFirst`
— and run in a detached worktree:

```text
--- FAIL: TestGatewayCredentialServesARequestWithNoBYOK (0.00s)
    aon3_failbefore_test.go:84:
        	Error:      	Received unexpected error:
        	            	all models failed: Provider credentials are not configured.
FAIL	github.com/agentstation/starport/internal/router	0.367s
```

The store held a credential at scope `*` and the resolver was never asked for
it, which is exactly the gap AON3 closes.

### Verifier

```text
$ bash scripts/verify-auth-onboarding.sh
PASS AON-V01 internal/tenant owns the tenant model
PASS AON-V02 tenant storage namespace is versioned tenant:v1:
PASS AON-V03 composition ensures the canonical default tenant
PASS AON-V04 a tenant carries the credential strategy the operator governs
PASS AON-V05 the limits vocabulary has its own owner, shared by key and tenant
PASS AON-V06 identity.APIKey carries a tenant
PASS AON-V07 the request context carries a tenant identity
PASS AON-V08 no controller derives a credential scope from an API key ID
PASS AON-V09 the provider credential package is named for all three sources
PASS AON-V10 no package still calls the whole credential subsystem byok
PASS AON-V11 the BYOK plane is scoped to a tenant, not to a key
PASS AON-V12 the gateway credential plane has a named scope
PASS AON-V13 credential resolution consults the gateway source
FAIL AON-V14 an operator applies a gateway credential on the provider route
...
Summary: 13 passed, 13 failed
```

Thirteen passing is the AON3 target in the plan's verifier table. The remaining
13 belong to AON4 through AON10 and are expected red.

### Tests

| Test | Package | What it proves |
| --- | --- | --- |
| `TestGatewayCredentialServesARequestWithNoBYOK` | `internal/router` | A credential stored at scope `*` serves a request that has no BYOK credential; the gateway scope is the only stored scope asked |
| `TestBYOKWinsOverAGatewayCredentialUnderBYOKFirst` | `internal/router` | With both planes populated, `byok_first` serves from the tenant's own and never reads the gateway scope or the environment |
| `TestBYOKOnlyNeverReachesAGatewayCredential` | `internal/router` | A `byok_only` tenant with a populated gateway plane gets the not-configured failure; the gateway scope is never read and no attempt is paid for |
| `TestNoCredentialInAnySourceReportsNotConfigured` | `internal/router` | An exhausted request consults all three planes in order and returns the one external not-configured shape, not a panic |
| `TestGatewayCredentialIsAttributedToTheOperator` | `internal/router` | Environment and gateway both record `CredentialOwnerOperator`; only BYOK records the tenant |
| `TestUnreachableStoredPlanesNeverWidenAStrategy` | `internal/router` | Dropping unreachable planes leaves `byok_only` with an empty source list rather than an operator credential |
| `TestEffectiveStrategyNarrowsButNeverWidens` | `.../keyring` | Absent metadata inherits the account strategy; narrowing and reordering are allowed; widening returns `ErrStrategyWidens`; unknown and non-string values return `ErrInvalidStrategy` |
| `TestTenantScopeNamesTheAccount` | `.../keyring` | `TenantScope` names the account and is never the gateway scope |
| `TestStrategyOrdersAllThreeSources` | `.../keyring` | Rewritten for three sources: `byok_only` withholds both operator planes, and the failure shape does not leak whether an operator credential exists |
| `TestAuthenticatedRequestCarriesItsTenantCredentialStrategy` | `internal/server` | A key issued to a `byok_only` account arrives at the handler under that strategy |
| `TestUnreadableTenantStillServesTheRequest` | `internal/server` | A failing tenant reader and an unwired one both serve the request under the default policy |
| `TestCredentialStrategyRefusalsAreDistinguishable` | `.../controllers` | A widening key answers 403 `permission_error`; an unparsable one answers 400 `invalid_request_error`; neither reaches the proxy |
| `TestKeyMayNarrowItsTenantStrategy` | `.../controllers` | A key stamped `byok_only` under an `operator_first` account is served under `byok_only` |
| `TestKeyWithoutStrategyInheritsTheTenantStrategy` | `.../controllers` | A key with no strategy metadata under a `byok_only` account runs under `byok_only`, not the default |

### Repository gates

```text
go build ./...                                     clean
go test ./...                                      no failures
go test -race ./internal/router/... ./internal/providers/... \
        ./internal/credentials/... ./internal/server/... \
        ./internal/tenant/...                      no failures
go vet ./...                                       clean
make lint                                          0 issues
make build                                         Build complete: ./starport
bash scripts/verify-package-layout.sh              package-layout verification passed
bash scripts/verify-dependency-direction.sh        Summary: 6 passed, 0 failed
bash scripts/test-dependency-direction-verifier.sh passed
bash scripts/verify-v1-architecture.sh             Summary: 12 passed, 0 failed
bash scripts/verify-starmap-ownership.sh           Summary: 12 passed, 0 failed
bash scripts/verify-catalog-driven-providers.sh    Summary: 19 passed, 0 failed
bash scripts/verify-openrouter-parity.sh           passed
bash scripts/verify-console-modernization.sh       passed
bash scripts/verify-readme-quickstart.sh           passed
bash scripts/verify-doc-links.sh                   passed
bash scripts/test-doc-link-verifier.sh             passed
bash scripts/verify-auth-onboarding.sh             Summary: 13 passed, 13 failed
```

Three gates named `internal/providers/byok` by path and were updated to
`internal/providers/keyring`: `verify-starmap-ownership.sh`,
`verify-catalog-driven-providers.sh`, and `verify-v1-architecture.sh`. Two of
them were silently green against the missing directory, which is the failure
mode a path-scoped gate has when the path moves — `verify-v1-architecture.sh`
V06 was scanning a directory that no longer existed and reporting no match.
`verify-catalog-driven-providers.sh` CDP-V19 was renamed with the test it runs,
from "BYOK order and user-only noninterference are exact" to "credential
strategy source order is exact".

## What AON3 deliberately did not do

- No route applies a gateway credential yet. The plane is reachable in the
  router, but `/api/v1/providers/{provider}/credentials` does not exist. AON4
  owns it, along with `/api/v1/tenants/{tenant_id}/byok` and deleting the
  key-nested routes.
- The `OperatorCredentialGate` still guards only `SourceEnvironment`.
  `state.Store.OperatorMaterialReady` compares against the runtime projection's
  `materialVersion`, which a stored credential's `stored:<rev>` version can
  never match, so extending the gate to `SourceGateway` would be a no-op that
  implies protection the gateway does not have. Whether stored credentials get
  their own health state is a separate question from AON3's scope.
- `Tenant.Limits` is still not a ceiling. AON5 resolves the stricter of the
  tenant and key limit, and it reads the same tenant record this task put on
  the request context.
- The console still shows one undifferentiated credential surface. AON10 owns
  separating the gateway API key screen from the provider credential screens.
