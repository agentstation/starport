#!/usr/bin/env bash
# Guard for the auth and onboarding campaign (plan prefix AON). Each
# condition asserts one structural property of the target design:
#
#   - a gateway API key authenticates and owns nothing else,
#   - a provider credential comes from the environment, from the gateway,
#     or from an account, and only the account one is called BYOK,
#   - an account carries the limits and the credential policy that let an
#     operator govern use of a gateway credential,
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
no_dir() { ! test -d "$1"; }

# --- AON1: the account seam and the shared limits vocabulary ---
check AON-V01 "internal/account owns the account model" \
  test -f internal/account/model.go
check AON-V02 "account storage namespace is versioned account:v1:" \
  grep_q 'account:v1:' internal/account
check AON-V03 "composition ensures the canonical default account" \
  grep_q 'EnsureDefault' internal/app
check AON-V04 "an account carries the credential strategy the operator governs" \
  grep_q 'CredentialStrategy' internal/account
check AON-V05 "the limits vocabulary has its own owner, shared by key and account" \
  test -f internal/limits/limits.go

# --- AON2: a key belongs to an account ---
check AON-V06 "identity.APIKey carries an account" \
  grep_q 'AccountID' internal/identity/model.go
check AON-V07 "the request context carries an account identity" \
  grep_q 'Account' internal/server/requestctx
check AON-V08 "no controller derives a credential scope from an API key ID" \
  absent '"user:" *+' internal/server/controllers

# --- AON3: the three credential sources ---
check AON-V09 "the provider credential package is named for all three sources" \
  test -d internal/providers/keyring
check AON-V10 "no package still calls the whole credential subsystem byok" \
  no_dir internal/providers/byok
check AON-V11 "the BYOK plane is scoped to an account, not to a key" \
  grep_q 'AccountScope' internal/providers/keyring
check AON-V12 "the gateway credential plane has a named scope" \
  grep_q 'GatewayScope' internal/providers/keyring
check AON-V13 "credential resolution consults the gateway source" \
  grep_q 'SourceGateway' internal/router

# --- AON4: credential routes ---
check AON-V14 "an operator applies a gateway credential on the provider route" \
  grep_q '{provider}/credentials' internal/server/routes.go
check AON-V15 "the account-brought credential route is named byok" \
  grep_q '/byok' internal/server/routes.go
check AON-V16 "the key-nested credential routes are gone" \
  absent 'provider-keys' internal/server/routes.go

# --- AON5: account governance ---
# The condition names the controller symbol and not the path. The BYOK plane is
# mounted at /accounts/{account_id}/byok, so a path match would report this
# condition green before a single admin account route exists. The account plane
# is its own controller rather than a growth of AdminController, so the symbol
# is Accounts.List.
check AON-V17 "admin account routes are registered" \
  grep_q 'Accounts.List' internal/server/routes.go
check AON-V18 "the budget path resolves an account limit" \
  grep_q 'AccountScope' internal/server/budget.go

# --- AON6: the authentication mode ---
check AON-V19 "config owns the gateway authentication mode" \
  grep_q 'AuthMode' internal/config
check AON-V20 "serve and dev accept --no-auth" \
  grep_q 'no-auth' internal/cli
check AON-V21 "startup validation holds the non-loopback tripwire" \
  grep_q 'loopback' internal/config/validation.go

# --- AON7: the console authentication switch ---
# The condition names the write handler and not the path. AON6 already mounts
# the public GET /auth/mode, so a path match would report this condition green
# two tasks before the console can change anything. What AON7 owns is the
# admin write, so the symbol is Auth.SetMode.
check AON-V22 "the admin authentication-mode route is registered" \
  grep_q 'Auth.SetMode' internal/server/routes.go

# --- AON8: the local admin token ---
check AON-V23 "internal/localauth owns the local admin token" \
  test -f internal/localauth/token.go
check AON-V24 "the starport auth command group exists" \
  grep_q 'Name: *"auth"' internal/cli

# --- AON9: launch tickets and sessions ---
check AON-V25 "the launch route consumes a one-time ticket" \
  grep_q '/launch' internal/server/routes.go

# --- AON10: console separation ---
check AON-V26 "no provider screen links to /keys for credentials" \
  absent 'to="/keys"' console/src/components/providers

printf 'Summary: %d passed, %d failed\n' "$pass" "$fail"
test "$fail" -eq 0
