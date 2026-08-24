# AON4 Credential routes

Date: 2026-08-23
Plan: [Auth and Onboarding](../../plans/auth-and-onboarding-plan.html)
Verifier: `scripts/verify-auth-onboarding.sh`, AON-V14 through AON-V16

## What changed

AON3 named the three credential sources and made the operator's stored plane
resolvable. Nothing could reach it: the only credential routes the gateway
served were nested under a gateway API key, at
`/api/v1/keys/{key_id}/provider-keys`. That path taught the conflation the whole
campaign exists to undo — it says a provider credential is a property of a
bearer token — and it left an operator with no route at all for applying a
credential to the deployment.

AON4 replaces one surface with two, each addressed by whoever owns what it
holds.

| Plane | Route | Guard | Scope written |
| --- | --- | --- | --- |
| Gateway credential (operator) | `/api/v1/providers/{provider}/credentials` | `admin` | `keyring.GatewayScope` |
| BYOK (tenant) | `/api/v1/tenants/{tenant_id}/byok[/{provider}]` | tenant access + `provider_keys:*` or `admin` | `keyring.TenantScope(id)` |

Neither path contains a key ID. The operator route does not require the
operator to hold a gateway API key of their own, which is the point: a
deployment credential is not a property of any key, so minting one to apply it
would reintroduce the association in a different place.

### One controller, two planes

`ProviderKeysController` is deleted. `ProviderCredentialsController` replaces
it and serves both planes, because they differ in exactly two things — the
scope they address and who may reach them — and in nothing else:

```go
// A gateway credential belongs to the operator and serves the whole
// deployment. A BYOK credential belongs to one tenant and serves only that
// tenant. They differ in who owns them and therefore in which scope holds
// them, and in nothing else, so one controller serves both and the scope is
// always named by the route rather than derived from the gateway API key that
// carried the request.
```

The per-surface methods are thin adapters over shared implementations, so the
two planes cannot drift in how they look a provider up, decode a body, or fail:

```go
func (h *ProviderCredentialsController) GatewayPut(w http.ResponseWriter, r *http.Request) {
	h.put(w, r, keyring.GatewayScope)
}

func (h *ProviderCredentialsController) BYOKPut(w http.ResponseWriter, r *http.Request) {
	h.put(w, r, byokScope(r))
}

// byokScope names the account the route addresses. RequireTenantAccess has
// already decided the caller may reach it.
func byokScope(r *http.Request) string {
	return keyring.TenantScope(chi.URLParam(r, "tenant_id"))
}
```

`PUT` is an upsert on both surfaces. An operator rotating a deployment
credential is stating what the credential should be, not asking whether one
already exists, and making them find out first would be the only reason to
split create from update.

### The operator reaches a tenant's plane, by holding admin

`RequireTenantAccess` guards every route addressed by account:

```go
// RequireTenantAccess guards a route addressed by account. A caller reaches
// its own account, and an operator holding admin reaches any account, because
// applying a credential on a tenant's behalf is a support operation an
// operator has to be able to perform. Nothing else passes.
//
// An operator naming an account that does not exist gets 404 rather than a
// silent write into a scope no tenant owns.
```

The 404 on an unknown account is the part worth stating. Without the existence
check, an operator's typo would encrypt a credential at `tenant:acmee` — a
scope with no owner, invisible to every tenant, and indistinguishable from
success.

The `admin` scope also appears in each BYOK route's scope list. Only `*` is a
wildcard in `identity.APIKey.HasScope`, so an operator holding `admin` and not
the tenant's own `provider_keys:read` would pass `RequireTenantAccess` and then
be refused by the inner scope check — an operator locked out of the support
operation the middleware just admitted them to.

### The usage endpoint moves to its real owner, and stays key-scoped

The plan's step 5 said "move the usage comparison endpoints to the tenant
path." That is not implementable inside this task's owning seam, and the
deviation is deliberate.

`usage.Record` and `usage.Query` carry `KeyID` and no tenant dimension at all
(`internal/usage/model.go`, `internal/usage/repository.go`). Serving per-tenant
usage means adding a tenant field to the record and an index to the repository
— a storage change in a package AON4 does not own, on the hot recording path.

What shipped instead:

- The aggregation moved from the deleted credential controller onto
  `ActivityController`, which already holds `usage.Repository`. Usage reporting
  belongs to the seam that owns usage records, not to the one that owns
  credentials.
- The route is `GET /api/v1/keys/{key_id}/usage/providers`, still key-scoped
  and still guarded by `requireKeyOwnership`, and the routes file says why it
  stays under the key.
- The name loses `provider-keys`. It groups by the provider that answered, not
  by the credential that paid — a usage record does not name a credential
  source — and the handler doc says so.
- `GET /api/v1/keys/{key_id}/usage/comparison`, a stub returning 410 Gone, is
  deleted outright rather than carried forward. Nothing has shipped, so there
  is no client to inform.

Tenant-scoped usage lands in AON5, where the tenant becomes a first-class
dimension of limits and spend and the storage change has an owner.

### The console stops calling deleted routes

`listProviderKeys`/`createProviderKey`/`deleteProviderKey`/`validateProviderKey`
become `listBYOKCredentials`/`putBYOKCredential`/`deleteBYOKCredential`/
`validateBYOKCredential`, addressed by tenant. `GatewayKey` gains `tenant_id`,
which the admin API already returned.

The credential panel stays on the key screen — AON10 owns the information
architecture — but its copy no longer claims the key owns what it shows:

> Credentials this account brings for itself. They belong to the `default`
> account, not to this key, so every key in the account uses them and rotating
> a key leaves them in place.

No client for the operator plane ships here. AON10 builds the provider screen
that applies a gateway credential; adding an unused client function now would
be speculative.

### A verifier that had gone green for the wrong reason

Mounting BYOK at `/tenants/{tenant_id}/byok` turned AON-V17 green:

```bash
check AON-V17 "admin tenant routes are registered" \
  grep_q '/tenants' internal/server/routes.go
```

The condition belongs to AON5 and no admin tenant route exists. A path match
cannot tell the two surfaces apart, so it now names the controller symbol,
which cannot appear by accident:

```bash
check AON-V17 "admin tenant routes are registered" \
  grep_q 'Admin.ListTenants' internal/server/routes.go
```

A gate that reports a condition green before its work exists cannot report the
regression it was written for.

## Evidence

### Fail-before

`TestKeyNestedCredentialRoutesAreGone` enumerates all eight retired routes and
asserts each is absent from the route table. All eight failed on the baseline
(`71cd24e`):

```text
--- FAIL: TestKeyNestedCredentialRoutesAreGone (1.81s)
    --- FAIL: .../GET_/api/v1/keys/acme-key/provider-keys
    --- FAIL: .../POST_/api/v1/keys/acme-key/provider-keys
    --- FAIL: .../GET_/api/v1/keys/acme-key/provider-keys/openai
    --- FAIL: .../PUT_/api/v1/keys/acme-key/provider-keys/openai
    --- FAIL: .../DELETE_/api/v1/keys/acme-key/provider-keys/openai
    --- FAIL: .../POST_/api/v1/keys/acme-key/provider-keys/openai/validate
    --- FAIL: .../GET_/api/v1/keys/acme-key/usage/provider-keys
    --- FAIL: .../GET_/api/v1/keys/acme-key/usage/comparison
```

The first version of this test asserted only the 404 status and passed on the
baseline for six of the eight sub-cases — the controller answered "Provider key
not found" for a provider that had no credential, which is a 404 from a live
route. The assertion now also requires the router's own body, so a controller
404 cannot pass for a deleted route:

```go
assert.Contains(t, recorder.Body.String(), "The requested endpoint does not exist",
	"the path must be gone from the route table, not answered by a controller")
```

### Verifier

```text
$ bash scripts/verify-auth-onboarding.sh
...
PASS AON-V14 an operator applies a gateway credential on the provider route
PASS AON-V15 the tenant-brought credential route is named byok
PASS AON-V16 the key-nested credential routes are gone
FAIL AON-V17 admin tenant routes are registered
...
Summary: 16 passed, 10 failed
```

Sixteen passing is the AON4 target in the plan's verifier table. The remaining
10 belong to AON5 through AON10 and are expected red.

### Tests

| Test | Package | What it proves |
| --- | --- | --- |
| `TestKeyNestedCredentialRoutesAreGone` | `internal/server` | All eight key-nested credential and usage paths are absent from the route table, answered by the router's not-found handler rather than a controller |
| `TestOperatorAppliesAGatewayCredentialResolutionCanRead` | `internal/server` | A `PUT` on the provider route lands at `keyring.GatewayScope` and `ResolveStoredMaterial` reads it back; a second `PUT` rotates it; `DELETE` leaves `ErrKeyNotFound` |
| `TestGatewayCredentialRouteRefusesANonAdmin` | `internal/server` | A tenant key gets 403 on every verb of the operator plane, and nothing is stored |
| `TestBYOKBelongsToItsTenant` | `internal/server` | A tenant reads and writes its own plane; a second tenant is refused on those paths and the stored value is unchanged; an operator holding `admin` reaches it |
| `TestBYOKAndGatewayCredentialsAreSeparateStores` | `internal/server` | A write to either plane is invisible to the other |
| `TestNoCredentialResponseCarriesItsSecret` | `internal/server` | Seven response bodies across both planes contain neither secret, and the gateway read reports `has_credentials` instead |
| `TestCredentialSurfacesDegradeLoudlyWithoutAStore` | `.../controllers` | All nine routes on both planes answer 503 with a named reason when no credential store is configured, rather than an empty set that reads as "none applied" |
| `TestACredentialBodyMustCarryStringFields` | `.../controllers` | Malformed JSON, an absent or empty credential map, and a nested or numeric field value each answer 400 naming the field to fix |
| `TestASchemaRejectionIsTheCallersMistake` | `.../controllers` | A `keyring.ValidationError` answers 400 carrying the schema's complaint; any other store failure answers 500 echoing neither the credential nor the internal error |
| `TestACredentialResponseNeverCarriesItsSecret` | `.../controllers` | The read summary reports the provider and no credential material |
| `TestActivityByProviderAggregates` | `.../controllers` | The moved aggregation groups one key's records by serving provider, counts unpriced requests separately, and excludes another key's records |

`provider_keys_test.go` and `provider_keys_tenant_test.go` are retired. The
tenant file proved that the credential scope came from the request context
rather than the URL key ID; AON4 makes the scope an explicit path segment, so
its subject no longer exists, and the isolation it protected is now proven end
to end over real storage in `TestBYOKBelongsToItsTenant`. The handler CRUD
tests were replaced with the four controller tests above, which cover what the
wired HTTP suite cannot reach: an absent store, a malformed body, and a
rejected write.

### Repository gates

```text
go build ./...                                     clean
go test ./...                                      no failures
go test -race ./internal/server/...                no failures
go vet ./...                                       clean
make lint                                          0 issues
make build                                         Build complete: ./starport
pnpm -C console build                              built in 3.53s
npx tsc --noEmit (console)                         clean
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
bash scripts/verify-auth-onboarding.sh             Summary: 16 passed, 10 failed
```

`scripts/smoke-openrouter-sdks.sh` is UNVERIFIED — it needs live provider
credentials and reaches no route this task changed.

## What AON4 deliberately did not do

- Per-tenant usage. `usage.Record` has no tenant dimension, so the per-provider
  rollup stays key-scoped at `/api/v1/keys/{key_id}/usage/providers`. AON5 owns
  the storage change.
- No console screen applies a gateway credential. The route exists and is
  admin-guarded; AON10 builds the provider screen that uses it, and moves the
  BYOK panel off the gateway API key screen.
- `Tenant.Limits` is still not a ceiling, so an operator cannot yet govern how
  much of a gateway credential a tenant spends. That is AON5, and it is what
  makes the operator plane safe to share.
- Authentication is still unconditionally required. AON6 owns the mode.
