#!/usr/bin/env bash
# Guard for the console session grants campaign (plan prefix CSG). Each
# condition asserts one structural property of the target design:
#
#   - a console session is minted by a named, registered grant, and every
#     grant reaches one minting path,
#   - two machine-local grants ship, a launch ticket and the local admin
#     token a reader pastes,
#   - a third grant, an identity provider, is registered and inert, so the
#     enterprise path is a slot to fill rather than a seam to reopen,
#   - the console has one first-contact page, outside the shell, that
#     states its trust scope and never stores the token it accepts,
#   - the words "sign in" belong to the identity grant alone.
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

# --- CSG1: the grant seam ---
check CSG-V01 "internal/localauth owns the grant seam" \
  test -f internal/localauth/grant.go
check CSG-V02 "the three grant kinds are named in one place" \
  grep_q 'GrantIdentity' internal/localauth/grant.go
check CSG-V03 "a session records the grant that minted it" \
  grep_q 'Grant GrantKind' internal/localauth/session.go
check CSG-V04 "ticket redemption goes through the registered grant" \
  grep_q 'GrantTicket' internal/localauth/gate.go

# --- CSG2: the local admin token grant ---
check CSG-V05 "the local admin token is a registered grant" \
  test -f internal/localauth/grant_local_token.go
check CSG-V06 "the paste path compares in constant time via the token" \
  grep_q 'Authorizes' internal/localauth/grant_local_token.go
check CSG-V07 "the paste path obeys the token exposure rule" \
  grep_q 'AllowsExposure' internal/localauth/grant_local_token.go

# --- CSG3: the identity grant, registered and inert ---
check CSG-V08 "the identity grant is registered" \
  test -f internal/localauth/grant_identity.go
check CSG-V09 "the inert identity grant refusal is held by a contract test" \
  grep_q 'ErrIdentityProviderNotConfigured' \
  internal/localauth/grant_identity_test.go

# --- CSG4: the HTTP route ---
check CSG-V10 "the console session route accepts the local token grant" \
  test -f internal/server/controllers/console_session.go
check CSG-V11 "the route is registered" \
  grep_q '/console/session' internal/server/routes.go

# --- CSG5: reaching first contact without the shell ---
check CSG-V12 "the console has an auth route outside the shell" \
  test -f console/src/routes/auth.tsx

# --- CSG6: the auth CLI verbs ---
check CSG-V13 "starport auth can copy the token and open the console URL" \
  grep_q '"copy"' internal/cli/auth.go

# --- CSG7: the first-contact page ---
check CSG-V14 "the page states its trust scope in the page" \
  grep_q 'Local-only' console/src
check CSG-V15 "the gateway key card no longer stands in for first contact" \
  test ! -f console/src/components/overview/ConnectCard.tsx

# --- CSG8: the reserved words ---
check CSG-V16 "sign-in copy belongs to the identity grant alone" \
  absent 'sign-in link' internal/cli

printf 'Summary: %d passed, %d failed\n' "$pass" "$fail"
test "$fail" -eq 0
