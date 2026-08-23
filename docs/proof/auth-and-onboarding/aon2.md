# AON2 Key belongs to a tenant

Date: 2026-08-23
Plan: [Auth and Onboarding](../../plans/auth-and-onboarding-plan.html)
Verifier: `scripts/verify-auth-onboarding.sh`, AON-V06 through AON-V08

## What changed

`BaseHandler.getTenantID` returned the gateway API key ID, so every decision the
gateway called a tenant decision was really a key decision: which provider
credentials a request could reach, which response cache entries it shared, and
which limits applied. AON2 makes the tenant a value the key carries.

### The key names an account

`identity.APIKey` gains `TenantID`, and one named function decides what an unset
value means:

```go
// ResolveTenantID maps a key's stored tenant to the tenant a request runs
// under. An empty value resolves to the canonical tenant. This is a permanent
// contract rather than a compatibility shim: a key that names no account
// belongs to the default account, at issue time and at read time alike.
func ResolveTenantID(value string) string
```

Both sides of that contract are exercised. `Issuer.issue` resolves the tenant
before it generates a credential, so a stored key always names an account.
`APIKey.EffectiveTenantID` resolves again on read, so a record written by any
other path still answers with an account rather than an empty string.

### An unknown account is refused at issue time

The issuer holds a one-method collaborator rather than a repository:

```go
// TenantChecker reports whether a tenant exists. The issuer holds this
// rather than a tenant repository so the key concept never learns how an
// account is stored.
type TenantChecker interface {
	Exists(ctx context.Context, tenantID string) (bool, error)
}
```

`identity.WithTenantChecker` is a variadic option, and
`internal/server/controllers` supplies the tenant repository behind it. A key
issued against a missing account would authenticate and then resolve to no
limits and no credential policy, which is a worse failure than refusing it.
`identity.ErrUnknownTenant` and `tenant.ErrInvalidID` both answer 400, because
naming an account that does not exist is the caller's mistake.

`internal/setup.InitializeIdentity` deliberately keeps no checker. It never
accepts a caller-supplied account — the initial key is always issued with no
tenant — so a checker there would catch nothing, and setup's rollback contract
depends on it writing identity records and nothing else.

### The request carries both values, and they are different

`RequireAPIKey` puts the tenant on the context beside the key. Reading it goes
through one function:

```go
// TenantIDOrDefault returns the account the request runs under, falling back
// to the canonical tenant when no authenticated identity set one.
func requestctx.TenantIDOrDefault(ctx context.Context) string
```

This is the single seam AON6 extends when an operator disables authentication.
An unauthenticated gateway still has to attribute usage, apply limits, and
select credentials, and this is the only place that decides which account it
uses.

`proxy.ChatCompletionRequest` and `proxy.EmbeddingsRequest` now carry `TenantID`
and `KeyID` as separate fields. That split is the substance of the change rather
than a detail: `internal/proxy/usage_capture.go` fed `req.TenantID` into
`usage.Record.KeyID`, which worked only because the two were the same value.
Making `TenantID` the real account without splitting the field would have
silently re-keyed every usage record onto the account and broken per-key
budgets, the activity view, and the `AggregateAllKeys` rollup.

### Provider credentials follow the account

Seven call sites in `internal/server/controllers/provider_keys.go` built the
credential scope as `"user:" + apiKeyID`, taking the key ID from the URL path.
They now call one method:

```go
func (h *ProviderKeysController) tenantScope(ctx context.Context) string {
	return byok.UserScope(requestctx.TenantIDOrDefault(ctx))
}
```

`byok.UserScope` already took a parameter named `tenantID`; the controllers were
passing a key ID into it. AON3 renames the helper to `TenantScope` and changes
the stored prefix to `tenant:`. AON2 changes only what the scope is derived
from, which is what AON-V08 asserts.

**This is a deliberate behavior change.** Two keys in one account now share
provider credentials, and deleting a key no longer strands the credentials
applied through it. `internal/response/cache/identity.go` widens the same way:
the semantic cache key now scopes to the account, so two keys in one tenant
share cache entries. Both are the intended reading of a tenant.

### The import rule names the direction

`internal/identity` now imports `internal/tenant` for `DefaultID` and
`ValidateID`. The architecture test states which direction is legal:

```go
// A gateway API key belongs to a tenant, so identity reaches the account
// model for its ID rules and its canonical ID. The loop above holds the
// other direction closed: tenant may never reach identity, because an
// account exists whether or not a key names it.
```

`internal/tenant` stays in the repository-concept loop, which allows only
`internal/storage` and `internal/limits`, so the reverse import fails the gate.

## Evidence

### Fail-before

`TestTwoKeysInOneTenantShareARequestTenant` copied onto the AON1 head
(`6c033e8`) does not build:

```text
internal/server/tenant_identity_test.go:35:34: undefined: requestctx.TenantIDOrDefault
internal/server/tenant_identity_test.go:63:57: undefined: identity.WithTenantChecker
internal/server/tenant_identity_test.go:67:20: unknown field TenantID in struct literal of type identity.IssueRequest
FAIL	github.com/agentstation/starport/internal/server [build failed]
```

The baseline has no vocabulary for the assertion, which is the plan's predicted
fail-before shape.

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
FAIL AON-V09 the provider credential package is named for all three sources
...
Summary: 8 passed, 18 failed
```

Eight passing is the AON2 target in the plan's verifier table. The remaining 18
belong to AON3 through AON10 and are expected red.

### Tests

| Test | Package | What it proves |
| --- | --- | --- |
| `TestTwoKeysInOneTenantShareARequestTenant` | `internal/server` | Two keys issued to `acme` authenticate to one request tenant, while their key IDs stay distinct from each other and from the tenant |
| `TestKeyWithNoTenantResolvesToDefault` | `internal/server` | A stored key with no tenant authenticates and resolves to `default`, so the contract holds at read time and not only at issue time |
| `TestUnauthenticatedRequestTenantIsDecidedInOnePlace` | `internal/server` | A context with no identity resolves to `default`, pinning the seam AON6 extends |
| `TestIssuerRefusesAnUnknownTenant` | `internal/identity` | Issuing against a missing account returns `ErrUnknownTenant` and stores no key; the same issuer still accepts a request that names no account and stamps `default` |
| `TestIssuerRefusesAMalformedTenantID` | `internal/identity` | `acme corp`, `acme/eu`, `*`, and `tenant:acme` are all refused, because a tenant ID reaches a credential storage key |
| `TestAdminCreateKeyReportsTheOwningTenant` | `.../controllers` | The creation response carries `tenant_id`, resolved rather than empty |
| `TestAdminCreateKeyRefusesAnUnknownTenant` | `.../controllers` | The route answers 400, not 500, and stores nothing |
| `TestAdminCreateKeyRefusesAMalformedTenantID` | `.../controllers` | The four malformed IDs are refused at the HTTP boundary |
| `TestProviderCredentialScopeFollowsTheTenant` | `.../controllers` | Two different URL key IDs under one tenant produce one credential scope, and no scope contains a gateway API key ID |
| `TestProviderCredentialScopeSeparatesTenants` | `.../controllers` | One URL key ID under two tenants produces two scopes, so the widened scope still isolates |
| `TestProviderCredentialScopeFallsBackToTheDefaultTenant` | `.../controllers` | A request with no identity scopes to `default` rather than to an empty scope |
| `TestChatCompletionWritesUsageRecord` | `internal/proxy` | Extended: the fixture now sets a tenant and a key ID that differ, and the record carries the key ID and asserts it is not the tenant |
| `TestNewRequiresReadyDependencies/tenants` | `internal/server` | The server refuses to start without a tenant repository |

### Repository gates

```text
go build ./...                                clean
go test ./...                                 no failures
go test ./internal/server/... ./internal/identity/... \
        ./internal/tenant/... ./internal/proxy/... -race   no failures
go vet ./...                                  clean
make lint                                     0 issues
bash scripts/verify-package-layout.sh         package-layout verification passed
bash scripts/verify-dependency-direction.sh   Summary: 6 passed, 0 failed
bash scripts/verify-v1-architecture.sh        Summary: 12 passed, 0 failed
bash scripts/verify-starmap-ownership.sh      Summary: 12 passed, 0 failed
bash scripts/verify-auth-onboarding.sh        Summary: 8 passed, 18 failed
```

## What AON2 deliberately did not do

- The credential scope still reads `user:<tenant_id>` in storage. AON3 owns the
  rename to `tenant:<tenant_id>`, the package rename from `byok` to `keyring`,
  and the three named sources. AON2 changed only what the scope is derived from.
- `Tenant.CredentialStrategy` is still stored and not enforced. AON3 translates
  it into eligible scopes in `internal/router`.
- `Tenant.Limits` is still not a ceiling. AON5 resolves the stricter of the
  tenant and key limit.
- No route creates or lists a tenant. AON5 registers the admin tenant routes,
  so today every key resolves to `default` unless a caller names an account the
  console cannot yet create.
- The console shows `tenant_id` on a key only because the admin API returns it.
  AON10 owns the console information architecture.
