#!/usr/bin/env bash
# Guard for the auth and onboarding campaign (plan prefix AON). Each
# condition asserts one structural property of the target design:
#
#   - a gateway API key authenticates and owns nothing else,
#   - provider credentials belong to the deployment or to a tenant,
#   - authentication is required unless an operator disables it,
#   - the console reaches the gateway without holding a gateway key.
#
# It reports every condition and exits nonzero while any condition fails.
set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

pass=0
fail=0

check() {
  local id="$1" desc="$2"
  shift 2
  if "$@" >/dev/null 2>&1; then
    printf 'PASS %s %s\n' "$id" "$desc"
    pass=$((pass + 1))
  else
    printf 'FAIL %s %s\n' "$id" "$desc"
    fail=$((fail + 1))
  fi
}

grep_q() { grep -Rq -- "$1" "${@:2}"; }
absent() { ! grep -Rq -- "$1" "${@:2}"; }

# --- AON1: the tenant seam ---
check AON-V01 "internal/tenant owns the tenant model" \
  test -f internal/tenant/model.go
check AON-V02 "tenant storage namespace is versioned tenant:v1:" \
  grep_q 'tenant:v1:' internal/tenant
check AON-V03 "composition ensures the canonical default tenant" \
  grep_q 'EnsureDefault' internal/app

# --- AON2: a key belongs to a tenant ---
check AON-V04 "identity.APIKey carries a tenant" \
  grep_q 'TenantID' internal/identity/model.go
check AON-V05 "the request context carries a tenant identity" \
  grep_q 'Tenant' internal/server/requestctx
check AON-V06 "no controller derives a credential scope from an API key ID" \
  absent '"user:" *+' internal/server/controllers

# --- AON3: credential planes ---
check AON-V07 "the tenant credential plane uses a tenant: scope prefix" \
  grep_q 'tenantScopePrefix' internal/providers/byok
check AON-V08 "the gateway credential plane has a named scope" \
  grep_q 'GatewayScope' internal/providers/byok
check AON-V09 "credential resolution consults the gateway plane" \
  grep_q 'CredentialSourceGateway' internal/router

# --- AON4: credential routes ---
check AON-V10 "provider-owned gateway credential routes are registered" \
  grep_q '/credentials' internal/server/routes.go
check AON-V11 "tenant-owned credential routes are registered" \
  grep_q '/tenants' internal/server/routes.go
check AON-V12 "the key-nested credential routes are gone" \
  absent 'provider-keys' internal/server/routes.go

# --- AON5: the authentication mode ---
check AON-V13 "config owns the gateway authentication mode" \
  grep_q 'AuthMode' internal/config
check AON-V14 "serve and dev accept --no-auth" \
  grep_q 'no-auth' internal/cli
check AON-V15 "startup validation holds the non-loopback tripwire" \
  grep_q 'loopback' internal/config/validation.go

# --- AON6: the console authentication switch ---
check AON-V16 "the admin authentication-mode route is registered" \
  grep_q 'auth/mode' internal/server/routes.go

# --- AON7: the local admin token ---
check AON-V17 "internal/localauth owns the local admin token" \
  test -f internal/localauth/token.go
check AON-V18 "the starport auth command group exists" \
  grep_q 'Name: *"auth"' internal/cli

# --- AON8: launch tickets and sessions ---
check AON-V19 "the launch route consumes a one-time ticket" \
  grep_q '/launch' internal/server/routes.go

# --- AON9: console separation ---
check AON-V20 "no provider screen links to /keys for credentials" \
  absent 'to="/keys"' console/src/components/providers

printf 'Summary: %d passed, %d failed\n' "$pass" "$fail"
test "$fail" -eq 0
