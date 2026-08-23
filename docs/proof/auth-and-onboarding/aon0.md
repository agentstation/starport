# AON0 Baseline

Date: 2026-08-23
Baseline: `main` @ `1c2f9d2`
Plan: [Auth and Onboarding](../../plans/auth-and-onboarding-plan.html)

## What the baseline does today

The three conditions this campaign changes, each read from source.

### A gateway API key is the tenant identity

`internal/server/controllers/base.go:81`

```go
func (h *BaseHandler) getTenantID(ctx context.Context) string {
	if tenantID, ok := requestctx.GetAPIKeyID(ctx); ok {
		return tenantID
	}
	return ""
}
```

`internal/identity/model.go` has no tenant field. The API key ID is the only
tenant value the request carries.

### A provider credential belongs to a gateway API key

`internal/server/controllers/provider_keys.go` builds the credential scope by
concatenating the key ID at seven call sites: lines 51, 151, 182, 238, 273,
300, and 330. Line 51 is representative.

```go
keys, err := h.providerKeys.ListKeys(ctx, "user:"+apiKeyID)
```

`internal/router/credential_policy.go:104` resolves the same value at
inference time through `byok.UserScope(p.tenantID)`, where
`UserScope(tenantID) = "user:" + tenantID` (`internal/providers/byok/keys.go:118`).

The routes carry the same shape (`internal/server/routes.go:83-99`):

```text
/api/v1/keys/{key_id}/provider-keys
/api/v1/keys/{key_id}/provider-keys/{provider}
/api/v1/keys/{key_id}/provider-keys/{provider}/validate
```

Deleting a gateway key therefore strands its provider credentials.

### The gateway-wide credential plane exists and has no consumer

`internal/providers/byok/provider_keys.go:298` writes a record with
`Scope: "*"`, and line 408 lists that scope. `credentials.ProviderKey.IsGlobal`
(`internal/credentials/model.go:39`) reports it. A repository-wide search finds
no caller of `SetGlobalKey`, `GetGlobalKey`, `DeleteGlobalKey`,
`ListGlobalKeys`, or `IsGlobal` outside the `byok` package. No route reaches
the plane, and `credentialPolicy.resolve` never consults it.

### Authentication cannot be turned off

`internal/server/routes.go:27` and `:56` apply `s.requireAPIKey` to every `/v1`
and `/api/v1` route. `RequireAPIKey` (`internal/server/middleware.go:108-255`)
returns 401 on a missing key with no conditional path. `internal/config` holds
no authentication-mode field.

### The console holds a gateway key

`console/src/lib/api.ts:4` stores the key in the browser:

```ts
const KEY_STORAGE = "starport.apiKey";
```

`console/src/routes/keys.tsx:642` opens the BYOK section inside the gateway-key
detail view, keyed by `["provider-keys", apiKey.id]` (line 659).
`console/src/components/providers/ProviderDetail.tsx` sends the user to the keys
page to "attach your own provider key to a gateway key", with a
`<Link to="/keys">Manage API Keys</Link>`.

## Fail-before evidence

`scripts/verify-auth-onboarding.sh` was authored red with 20 conditions.

```text
$ bash scripts/verify-auth-onboarding.sh
FAIL AON-V01 internal/tenant owns the tenant model
FAIL AON-V02 tenant storage namespace is versioned tenant:v1:
FAIL AON-V03 composition ensures the canonical default tenant
FAIL AON-V04 identity.APIKey carries a tenant
FAIL AON-V05 the request context carries a tenant identity
FAIL AON-V06 no controller derives a credential scope from an API key ID
FAIL AON-V07 the tenant credential plane uses a tenant: scope prefix
FAIL AON-V08 the gateway credential plane has a named scope
FAIL AON-V09 credential resolution consults the gateway plane
FAIL AON-V10 provider-owned gateway credential routes are registered
FAIL AON-V11 tenant-owned credential routes are registered
FAIL AON-V12 the key-nested credential routes are gone
FAIL AON-V13 config owns the gateway authentication mode
FAIL AON-V14 serve and dev accept --no-auth
FAIL AON-V15 startup validation holds the non-loopback tripwire
FAIL AON-V16 the admin authentication-mode route is registered
FAIL AON-V17 internal/localauth owns the local admin token
FAIL AON-V18 the starport auth command group exists
FAIL AON-V19 the launch route consumes a one-time ticket
FAIL AON-V20 no provider screen links to /keys for credentials
Summary: 0 passed, 20 failed
exit=1
```

Three conditions are negative assertions that fail because the baseline holds
the behavior they forbid: AON-V06 finds the `"user:" +` concatenation in the
controllers, AON-V12 finds `provider-keys` in the route table, and AON-V20
finds the `to="/keys"` credential link on the provider screen.

## Scope of this task

No Go source, console source, or configuration changed. This task added the
plan, the proof root, and the verifier.

## Reference: the Nimbus local-auth pattern

The plan adopts two Nimbus mechanisms. Recorded here so a later session does
not re-derive them.

- `crates/nimbus-operator/src/token.rs` holds a local admin token record with a
  version, the token, a generation counter, an issue time, a scope, and a
  rotation time. It auto-mints under a file lock on first boot, compares in
  constant time, and treats a never-rotated auto-minted token as stale, so the
  non-loopback bind tripwire refuses to expose it.
- `crates/nimbus-operator/src/access.rs` mints a short-lived one-time launch
  ticket. The CLI opens `/ui/launch?lt=<ticket>`, and the server exchanges the
  ticket for a signed session cookie. The browser never sees the admin token,
  and rotation moves every live session to `revoked_sessions`.
- `crates/nimbus-cli/src/start/first_boot.rs` prints the first-run banner once,
  gated on the absence of a stamp file, after the console answers.

Nimbus uses `demo` as a tenant ID in its audit test fixtures. That is a test
value. Starport's canonical tenant is `default` (plan decision D2).
