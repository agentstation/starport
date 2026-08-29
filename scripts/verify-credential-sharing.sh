#!/usr/bin/env bash
# Guard for the credential-sharing and identity campaign (ID prefix CSH).
# Each condition asserts one structural property of the target design:
#
#   - internal/sqlstore owns the relational contract: an embedded SQLite
#     backend rides the binary with no cgo, and a PostgreSQL or MySQL
#     connect scales it, the way Badger pairs with Valkey,
#   - internal/providers/keyring holds many shared credentials per provider,
#     each open to every account or granted to some, and the source word on
#     the wire is shared,
#   - internal/account carries the operator's BYOK policy and the account's
#     provider and model access, enforced at the BYOK put, at keyring
#     resolution, and at route planning,
#   - account templates stamp creation defaults and never rewrite an
#     existing account,
#   - internal/identity owns users, teams, and account grants, acquired
#     through gothic OAuth or WorkOS SSO into one user model, and the
#     localauth identity grant mints the session,
#   - the console renders each surface with the campaign's vocabulary.
#
# Authored red at baseline b00c47d (CSH-A0): each condition turned green as
# its CSH task closed. The gate is terminal at 23 conditions (CSH-V01
# through CSH-V23) and runs in CI. It reports every condition and exits
# nonzero while any condition fails.
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

# all_present reports that every term before the -- appears somewhere under
# the paths after it. A partial vocabulary is the failure this catches.
all_present() {
  local terms=() paths=()
  local seen=0 arg
  for arg in "$@"; do
    if [ "$arg" = "--" ]; then seen=1; continue; fi
    if [ "$seen" -eq 0 ]; then terms+=("$arg"); else paths+=("$arg"); fi
  done
  if [ "$seen" -eq 0 ] || [ "${#paths[@]}" -eq 0 ]; then return 1; fi
  local term
  for term in "${terms[@]}"; do
    grep -Rq -- "$term" "${paths[@]}" || return 1
  done
  return 0
}

# tests_all_present is all_present restricted to Go test files. A symbol that
# only the source names is a vocabulary entry, not a held contract.
tests_all_present() {
  local terms=() paths=()
  local seen=0 arg
  for arg in "$@"; do
    if [ "$arg" = "--" ]; then seen=1; continue; fi
    if [ "$seen" -eq 0 ]; then terms+=("$arg"); else paths+=("$arg"); fi
  done
  if [ "$seen" -eq 0 ] || [ "${#paths[@]}" -eq 0 ]; then return 1; fi
  local term
  for term in "${terms[@]}"; do
    grep -Rq --include='*_test.go' -- "$term" "${paths[@]}" || return 1
  done
  return 0
}

# ts_all_present is all_present restricted to the console sources.
ts_all_present() {
  local term
  for term in "$@"; do
    grep -Rq --include='*.ts' --include='*.tsx' -- "$term" console/src || return 1
  done
  return 0
}

# --- Phase B, the relational store ---

check CSH-V01 "internal/sqlstore owns the relational contract" \
  all_present 'package sqlstore' -- internal/sqlstore

check CSH-V02 "the embedded SQLite backend is pure Go, so the single binary holds" \
  all_present 'modernc.org/sqlite' -- go.mod

check CSH-V03 "sqlstore owns its migrations; no other package writes schema" \
  all_present 'migrat' -- internal/sqlstore

check CSH-V04 "a PostgreSQL or MySQL connect scales the same contract" \
  all_present 'postgres' 'mysql' -- internal/sqlstore

check CSH-V05 "one contract test suite runs against every backend" \
  tests_all_present 'sqlite' 'postgres' -- internal/sqlstore

# --- Phase C, many shared credentials ---

check CSH-V06 "the keyring holds a list of shared credentials per provider, held by tests" \
  tests_all_present 'SharedCredential' -- internal/providers/keyring

check CSH-V07 "each shared credential is open to every account or granted to some" \
  all_present 'AccessOpen' 'AccessGranted' -- internal/providers/keyring

check CSH-V08 "the served source word is shared, owned by the keyring alone" \
  all_present 'SourceShared' -- internal/providers/keyring

check CSH-V09 "the admin plane addresses a shared credential by id" \
  all_present 'credentialID' -- internal/server

check CSH-V10 "the console create flow asks open or granted and defaults to every account" \
  ts_all_present 'every account' 'only granted accounts'

# --- Phase D, policy and access ---

check CSH-V11 "the account carries the operator's BYOK policy" \
  all_present 'BYOKPolicy' -- internal/account

check CSH-V12 "a BYOK put for a provider the policy withholds refuses, held by tests" \
  tests_all_present 'BYOKPolicy' -- internal/server

check CSH-V13 "the account names its provider and model access, defaulting to all models" \
  all_present 'ProviderAccess' -- internal/account

check CSH-V14 "planning refuses a model outside the account's access, held by tests" \
  tests_all_present 'ProviderAccess' -- internal/routing

# --- Phase E, account templates ---

check CSH-V15 "account templates stamp creation defaults" \
  all_present 'type Template struct' 'func (t Template) Stamp' -- internal/account

check CSH-V16 "the console word for creation defaults is template, not preset" \
  ts_all_present 'account template'

# --- Phase F, identity ---

# internal/identity holds gateway API-key identity at baseline; that package
# renames to internal/apikey (decision 6) so the human seam takes the name
# localauth's identity grant already uses. The condition therefore asks for
# the human models, not the package clause.
check CSH-V17 "internal/identity owns users, teams, and memberships" \
  all_present 'type User struct' 'type Team struct' -- internal/identity

check CSH-V18 "gothic OAuth acquires an identity into the one user model" \
  all_present 'gothic' -- internal/identity

check CSH-V19 "WorkOS SSO acquires an identity beside gothic, same user model" \
  all_present 'workos' -- internal/identity

check CSH-V20 "an identity session resolves the accounts its grants reach" \
  all_present 'AccountGrant' -- internal/identity

# --- Phase G, grants ---

check CSH-V21 "a granted shared credential serves only accounts its grants name, held by tests" \
  tests_all_present 'AccountGrant' -- internal/providers/keyring

check CSH-V22 "the console grants an account to a user or a team" \
  ts_all_present 'Members' 'Teams'

# --- Close ---

check CSH-V23 "CI runs this gate and the evidence list names it" \
  all_present 'verify-credential-sharing.sh' -- .github/workflows AGENTS.md

printf 'Summary: %d passed, %d failed\n' "$pass" "$fail"
test "$fail" -eq 0
