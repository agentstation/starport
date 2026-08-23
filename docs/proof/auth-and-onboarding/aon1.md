# AON1 The tenant seam and the shared limits vocabulary

Date: 2026-08-23
Plan: [Auth and Onboarding](../../plans/auth-and-onboarding-plan.html)
Verifier: `scripts/verify-auth-onboarding.sh`, AON-V01 through AON-V05

## What changed

Two packages, and one import rule that keeps them honest.

### `internal/tenant` owns the account identity

A `Tenant` holds an ID, a name, `Limits`, a `CredentialStrategy`, metadata, an
active flag, and timestamps. The strategy is the field the campaign exists for:
it is where an operator says whether an account may spend the deployment's own
provider credentials.

```go
const (
	StrategyOperatorFirst CredentialStrategy = "operator_first"
	StrategyBYOKFirst     CredentialStrategy = "byok_first"
	StrategyBYOKOnly      CredentialStrategy = "byok_only"
)
```

`operator_first` spends environment, then gateway, then BYOK. `byok_first`
inverts the preference and still falls back. `byok_only` is the deny value: it
serves a tenant from its own credentials alone. AON3 translates these into
eligible credential scopes inside `internal/router`, so `internal/tenant` never
learns how a credential is stored and the credential package never learns about
the account model.

An empty strategy reads as `operator_first` rather than as a denial
(`EffectiveCredentialStrategy`). A zero value must not silently cut a tenant off
from every provider.

The repository is compare-and-swap versioned like every other concept
repository, under the `tenant:v1:` namespace. Two behaviors are specific to this
concept:

- `EnsureDefault` creates the canonical `default` tenant once. When a concurrent
  boot loses the create race it re-reads the winner instead of failing startup,
  so two processes against one store both come up.
- `Delete` refuses `DefaultID` with `ErrDefaultImmutable`, even on a correct
  revision. A gateway API key with no explicit tenant resolves to `default`;
  removing it would strand those keys.

`Update` stamps `CreatedAt` from the stored record and `UpdatedAt` from the
clock **before** validating, so the check reads the record the call actually
writes rather than the caller's payload.

### `internal/limits` owns the spend vocabulary

`Limits`, `RequestLimit`, `Budget`, `ValidInterval`, and the four validation
errors moved out of `internal/identity`. Both a gateway API key and a tenant
hold limits. A `tenant` package importing `identity` to reuse the type would
invert the ownership direction — the tenant is the owner, not the key.

The JSON tags are byte-identical to the `identity` versions, so the stored
`APIKey` record shape is unchanged. **No data moves.**

Seven files import the new package: `internal/tenant`, `internal/identity`
(model and issuer), `internal/server` (budget and two tests), and
`internal/server/controllers` (admin and its test).

### Composition ensures the default tenant

`internal/app/app.go` opens the tenant repository and calls `EnsureDefault`
before it opens the identity repository, because every gateway API key resolves
to a tenant.

### The import graph rule was extended, not relaxed

`internal/architecture/import_graph_test.go` held a rule that a
repository-owning concept may import `internal/storage` and nothing else inside
the module. That rule now reads storage **and** `internal/limits`, `tenant`
joins the governed set, and `internal/limits` itself is asserted to be a leaf
with no internal imports at all — so neither owner can reach the other through
the shared vocabulary.

## Evidence

### Verifier

```text
$ bash scripts/verify-auth-onboarding.sh
PASS AON-V01 internal/tenant owns the tenant model
PASS AON-V02 tenant storage namespace is versioned tenant:v1:
PASS AON-V03 composition ensures the canonical default tenant
PASS AON-V04 a tenant carries the credential strategy the operator governs
PASS AON-V05 the limits vocabulary has its own owner, shared by key and tenant
FAIL AON-V06 identity.APIKey carries a tenant
...
Summary: 5 passed, 21 failed
exit=1
```

Five passing is the AON1 target in the plan's verifier table. The remaining 21
belong to AON2 through AON10 and are expected red.

### Tests

`go test ./internal/tenant/...` — 6 tests, all passing. Each repository test
runs under `repotest.Run`, so every case executes against both the in-memory
and the Badger backend.

| Test | What it proves |
| --- | --- |
| `TestTenantRepositoryContract` | Create, read, schema version on the durable value, duplicate create conflicts, limits are cloned in and out so a caller cannot mutate the stored record through its pointer, update bumps the revision and keeps the original `CreatedAt`, stale-revision update and delete both conflict, delete then read is `ErrNotFound` |
| `TestEnsureDefaultIsIdempotent` | A cold store gets `default` with `operator_first`; a second call returns the identical record without bumping the revision or creating a second tenant |
| `TestEnsureDefaultUnderConcurrentBoot` | Eight concurrent `EnsureDefault` calls all succeed, all return revision 1, and the store holds exactly one tenant |
| `TestDefaultTenantCannotBeDeleted` | A correct-revision delete of `default` returns `ErrDefaultImmutable` and the tenant survives |
| `TestRepositoryRejectsInvalidTenants` | Five malformed IDs are refused, including `tenant:acme` and `*` — a tenant ID reaches a credential storage key, so a separator or a wildcard must never enter one — plus an empty name, an unknown strategy, and an invalid budget interval; the store stays empty |
| `TestCredentialStrategyPolicy` | An unset strategy reads as `operator_first`, and `byok_only` is the only value that denies operator credentials |

`go test ./internal/tenant/... -race` — passing, which is what makes the
concurrent-boot case meaningful.

### Repository gates

```text
go build ./...                          clean
go test ./...                           no failures
go vet ./...                            clean
make lint                               0 issues
bash scripts/verify-package-layout.sh   package-layout verification passed
bash scripts/verify-dependency-direction.sh   Summary: 6 passed, 0 failed
```

## What AON1 deliberately did not do

- No route, controller, or console screen reads a tenant yet. AON2 puts a
  tenant on `identity.APIKey` and into the request context; until then
  `getTenantID` still returns the API key ID.
- `CredentialStrategy` is stored and validated but not yet enforced. AON3 wires
  it into credential resolution.
- `Tenant.Limits` is stored and validated but not yet a ceiling. AON5 resolves
  the stricter of the tenant and key limit and applies it in the budget and
  rate-limit paths.

Each of these is a ledger row with its own verifier condition, so a field that
is carried but not honored cannot survive to the campaign's terminal state.
