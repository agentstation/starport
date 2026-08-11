#!/usr/bin/env bash

set -u
set -o pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
passed=0
failed=0

run_check() {
  local id=$1
  local description=$2
  local output
  shift 2

  if output=$("$@" 2>&1); then
    printf 'PASS %s %s\n' "$id" "$description"
    passed=$((passed + 1))
    return 0
  fi

  printf 'FAIL %s %s\n' "$id" "$description"
  if [[ -n $output ]]; then
    printf '%s\n' "$output" >&2
  fi
  failed=$((failed + 1))
  return 0
}

named_tests() {
  local packages=$1
  local pattern='^('
  local separator=''
  local test_name
  local listed
  shift

  for test_name in "$@"; do
    pattern+="${separator}${test_name}"
    separator='|'
  done
  pattern+=')$'

  listed=$(
    cd "$root" || exit 1
    # The package list is a verifier-owned constant.
    # shellcheck disable=SC2086
    go test $packages -list "$pattern"
  ) || return 1

  for test_name in "$@"; do
    grep -qx "$test_name" <<<"$listed" || {
      printf 'missing test: %s\n' "$test_name" >&2
      return 1
    }
  done

  (
    cd "$root" || exit 1
    # The package list is a verifier-owned constant.
    # shellcheck disable=SC2086
    go test $packages -run "$pattern" -count=1
  )
}

provider_neutral_bootstrap() {
  named_tests "./internal/cli ./internal/setup" \
    TestInitRejectsProviderFlag \
    TestLocalInitPersistsNoProviderCredential
}

in_memory_local_development() {
  named_tests "./internal/cli ./internal/app ./internal/storage" \
    TestDevUsesInMemoryBadger \
    TestDevBindsLoopbackOnly \
    TestDevPrintsGatewayKeyOnce
}

catalog_wide_registration() {
  named_tests "./internal/providers ./internal/registry ./internal/app" \
    TestCatalogProviderRegistersWithoutOperatorMaterial \
    TestNoAuthProviderRegistersWithoutMaterial
}

request_bound_endpoint_binding() {
  named_tests "./internal/catalog ./internal/routing ./internal/router ./internal/app" \
    TestTenantOnlyBindsEndpointFromTenantMaterial \
    TestOperatorAndTenantBindingsDoNotCross \
    TestUserOnlySkipsOperatorResolution
}

starmap_inference_contract() {
  named_tests "./internal/config ./internal/app" \
    TestCatalogCredentialEnvironmentPrecedence \
    TestInferenceCredentialsNeverEnterCatalogState
}

declared_cloud_discovery() {
  named_tests "./internal/credentials ./internal/providers ./internal/providerauth" \
    TestProviderReconcilerDiscoversGoogleDefault \
    TestGoogleDefaultProjectsCatalogProjectField \
    TestProviderReconcilerSkipsUndeclaredCloudChain
}

atomic_provider_reconciliation() {
  named_tests "./internal/providers ./internal/registry ./internal/app" \
    TestProviderReconcilerDiscoversAmbientKey \
    TestProviderReconcilerIntervalPublishesChangedGeneration \
    TestProviderReconcilerManualRefreshSharesInflight \
    TestProviderFailureDoesNotBlockOthers \
    TestProviderReconcilerCancellationStops \
    TestRuntimeGenerationDrainsConnectors
}

scoped_provider_state() {
  named_tests "./internal/providerstate ./internal/failure ./internal/availability ./internal/execution" \
    TestProviderStateProjectionContract \
    TestProviderStateRedactsCredentialMaterial \
    TestFailureTransitionsRespectDocumentedScope \
    TestMaterialVersionRecovery
}

authenticated_provider_operations() {
  named_tests "./internal/server" \
    TestAdminProviderStatusContract \
    TestAdminProviderRefreshContract \
    TestAdminProviderRoutesRequireAuthentication
}

credential_independent_readiness() {
  named_tests "./internal/app ./internal/server" \
    TestRuntimeStartsWithoutOperatorCredentials \
    TestReadinessIgnoresProviderCredentialAvailability
}

verified_quickstart() {
  ! grep -q 'STARPORT_BIN' "$root/README.md" &&
    ! grep -q 'init --provider' "$root/README.md" &&
    grep -q 'starport dev' "$root/README.md"
}

verified_release_readback() {
  test -x "$root/scripts/verify-automatic-provider-runtime-release.sh" &&
    "$root/scripts/verify-automatic-provider-runtime-release.sh"
}

run_check APR-V01 'provider-neutral bootstrap persists no provider credential' \
  provider_neutral_bootstrap
run_check APR-V02 'local development uses loopback and in-memory storage' \
  in_memory_local_development
run_check APR-V03 'catalog providers register without operator material' \
  catalog_wide_registration
run_check APR-V04 'request policy precedes endpoint binding and authentication' \
  request_bound_endpoint_binding
run_check APR-V05 'Starmap inference profiles and order drive resolution' \
  starmap_inference_contract
run_check APR-V06 'declared cloud chains run outside the inference hot path' \
  declared_cloud_discovery
run_check APR-V07 'provider reconciliation publishes atomic generations' \
  atomic_provider_reconciliation
run_check APR-V08 'provider state and failures remain safe and scoped' \
  scoped_provider_state
run_check APR-V09 'admin routes report state and trigger reconciliation' \
  authenticated_provider_operations
run_check APR-V10 'gateway readiness ignores provider credential availability' \
  credential_independent_readiness
run_check APR-V11 'the tested quickstart uses current plain commands' \
  verified_quickstart
run_check APR-V12 'repository and public release readback gates pass' \
  verified_release_readback

printf 'Summary: %d passed, %d failed\n' "$passed" "$failed"
test "$failed" -eq 0
