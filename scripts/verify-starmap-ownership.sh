#!/usr/bin/env bash

set -u
set -o pipefail

STARPORT_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
STARMAP_ROOT=${STARMAP_OWNERSHIP_STARMAP_ROOT:-"$(dirname "$STARPORT_ROOT")/starmap"}

passed=0
failed=0

run_check() {
  local id=$1
  local description=$2
  shift 2

  if "$@" >/dev/null 2>&1; then
    printf 'PASS %s %s\n' "$id" "$description"
    passed=$((passed + 1))
  else
    printf 'FAIL %s %s\n' "$id" "$description"
    failed=$((failed + 1))
  fi
}

named_tests() {
  local root=$1
  local packages=$2
  shift 2
  local pattern='^('
  local separator=''
  local test_name
  for test_name in "$@"; do
    pattern+="${separator}${test_name}"
    separator='|'
  done
  pattern+=')$'

  local listed
  listed=$(
    cd "$root" || exit 1
    # The package list is a fixed verifier-owned value, not caller input.
    # shellcheck disable=SC2086
    go test $packages -list "$pattern"
  ) || return 1
  for test_name in "$@"; do
    grep -qx "$test_name" <<<"$listed" || return 1
  done
  (
    cd "$root" || exit 1
    # The package list is a fixed verifier-owned value, not caller input.
    # shellcheck disable=SC2086
    go test $packages -run "$pattern" -count=1
  )
}

starport_tests() {
  named_tests "$STARPORT_ROOT" "$@"
}

starmap_tests() {
  named_tests "$STARMAP_ROOT" "$@"
}

no_match() {
  local pattern=$1
  shift
  local status=0
  grep -R -n -E -- "$pattern" "$@" || status=$?
  [[ $status -eq 1 ]]
}

no_starport_match() {
  local pattern=$1
  shift
  local status=0
  grep -R -n -E --include='*.go' --exclude='*_test.go' -- "$pattern" "$@" || status=$?
  [[ $status -eq 1 ]]
}

connector_facts_are_catalog_derived() {
  no_starport_match \
    '(api\.openai\.com|api\.anthropic\.com|generativelanguage\.googleapis\.com|aiplatform\.googleapis\.com|api\.groq\.com|api\.mistral\.ai|localhost:11434|YOUR-DEPLOYMENT-NAME|claude-3-haiku|gemini-1\.5-flash|llama3-8b-8192|text-embedding-ada-002)' \
    "$STARPORT_ROOT/internal/providers/connectors" &&
    no_starport_match 'Model:[[:space:]]*"[a-z0-9-]+/"[[:space:]]*\+' \
      "$STARPORT_ROOT/internal/providers/connectors" &&
    no_starport_match '(resolveOperationBase|DefaultProviderConfig|streamGenerateContentAction|apiVersion|api_version)' \
      "$STARPORT_ROOT/internal/providers/connectors" &&
    test ! -e "$STARPORT_ROOT/starport.example.yaml" &&
    test ! -e "$STARPORT_ROOT/starport.example.simple.yaml"
}

current_architecture_is_snapshot_only() {
  no_match \
    '(Models\(ctx context\.Context\)|Health\(ctx context\.Context\)|health/model snapshots|Connectors can report live observations|Registry validation and health work)' \
    "$STARPORT_ROOT/docs/ARCHITECTURE.md" &&
    no_starport_match '(GetConnectorForModel|extractProviderFromModel)' \
      "$STARPORT_ROOT/internal/registry"
}

catalog_ownership_is_single_source() {
  connector_facts_are_catalog_derived && current_architecture_is_snapshot_only
}

test -d "$STARPORT_ROOT/internal" || {
  printf 'Starport root is invalid: %s\n' "$STARPORT_ROOT" >&2
  exit 2
}
test -d "$STARMAP_ROOT/pkg/catalogs" || {
  printf 'Starmap root is invalid: %s\n' "$STARMAP_ROOT" >&2
  exit 2
}

run_check O01 "active providers are the catalog and compiled primitive intersection" \
  starport_tests "./internal/app ./internal/catalog" \
  TestCatalogWideProviderActivation TestConfiguredProviderMissingCatalogFailsStartup
run_check O02 "primitive registries own inference transport and authentication dispatch" \
  starport_tests "./internal/architecture ./internal/providers/keyring" \
  TestStarportProductionHasNoProviderRoster \
  TestTransportAuthenticationRegistriesUsePrimitives \
  TestValidateKeyUsesCatalogCredentialContracts
run_check O03 "catalog-acquisition and inference credential planes are isolated" \
  starport_tests "./internal/app ./internal/catalog" \
  TestAuthPlanesAreIsolated TestStarmapAcquisitionPublishesRefresh
run_check O04 "Google inference credentials never enter a URL" \
  no_starport_match '(\?key=|query\.Set\("key")' \
  "$STARPORT_ROOT/internal/providers/connectors"
run_check O05 "model and endpoint discovery use one routable snapshot" \
  starport_tests "./internal/proxy ./internal/catalog" \
  TestSnapshotOnlyDiscovery TestModelDiscoveryRetainsOneCatalogGeneration
run_check O06 "embedding routes require catalog and adapter capabilities" \
  starport_tests "./internal/proxy ./internal/routing" \
  TestEmbeddingRequiresCatalogAndAdapterCapability
run_check O07 "provider model IDs stay exact and opaque" \
  starport_tests "./internal/providers/connectors" \
  TestExactProviderModelIDIsOpaque TestOfferingEndpointSelectsProtocol
run_check O08 "connectors and current architecture contain no duplicate catalog ownership" \
  catalog_ownership_is_single_source
run_check O09 "prompt cache and price behavior use exact offerings" \
  starport_tests "./internal/proxy ./internal/providers/keyring" \
  TestOfferingCacheCapability TestOfferingPriceHasNoFallback
run_check O10 "Starport has no provider-wide cache or sample-price tables" \
  no_starport_match '(supportedCacheControlProviders|getStandardCost|default.*Cost)' \
  "$STARPORT_ROOT/internal/proxy" "$STARPORT_ROOT/internal/providers/keyring"
run_check O11 "Starmap owns provider acquisition auth and service facts" \
  starmap_tests "./pkg/catalogs ./acquisition ./internal/auth/... ./internal/embedded" \
  TestCatalogAcquisitionAuthContract TestCloudCredentialChainSelection \
  TestAcquisitionCredentialsNeverSerialize TestProviderInferenceContract \
  TestProviderOfferingServiceCapabilities TestBindOfferingEndpoint TestEmbeddedProviderContracts
run_check O12 "catalog fact mutations flow without new Starport conditionals" \
  starport_tests "./internal/catalog ./internal/app ./internal/proxy" \
  TestStarmapFactMutationContract

printf 'Summary: %d passed, %d failed\n' "$passed" "$failed"
test "$failed" -eq 0
