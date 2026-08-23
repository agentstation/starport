#!/usr/bin/env bash

set -u
set -o pipefail

STARPORT_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
STARMAP_ROOT=${CATALOG_DRIVEN_STARMAP_ROOT:-"$(dirname "$STARPORT_ROOT")/starmap"}

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
  local root=$1
  local packages=$2
  local pattern='^('
  local separator=''
  local test_name
  local listed
  shift 2

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

starport_tests() {
  named_tests "$STARPORT_ROOT" "$@"
}

starmap_tests() {
  named_tests "$STARMAP_ROOT" "$@"
}

no_production_go_match() {
  local pattern=$1
  local status=0
  shift

  grep -R -n -E --include='*.go' --exclude='*_test.go' -- "$pattern" "$@" || status=$?
  [[ $status -eq 1 ]]
}

credential_contract() {
  starmap_tests "./pkg/catalogs ./internal/embedded" \
    TestCatalogCredentialContract \
    TestCatalogCredentialRoundTrip \
    TestCatalogCredentialCopyIsolation
}

credential_planes_are_isolated() {
  starmap_tests "./pkg/catalogs ./acquisition ./internal/embedded" \
    TestCatalogCredentialPlanesAreIsolated \
    TestCatalogCredentialsNeverSerializeValues &&
    starport_tests "./internal/app ./internal/catalog" \
      TestInferenceCredentialsNeverEnterCatalogState
}

provider_yaml_uses_credential_schema() {
  starmap_tests "./internal/embedded" \
    TestEmbeddedProviderCredentialSchemaContract
}

environment_precedence() {
  starmap_tests "./internal/auth" \
    TestCatalogCredentialEnvironmentPrecedence &&
    starport_tests "./internal/config ./internal/credentials" \
      TestCatalogCredentialEnvironmentPrecedence
}

alias_collisions_fail_before_reads() {
  starmap_tests "./pkg/catalogs ./internal/auth" \
    TestCredentialAliasCollisionsFailBeforeEnvironmentReads &&
    starport_tests "./internal/config ./internal/app" \
      TestCredentialAliasCollisionsFailBeforeConnectorConstruction
}

explicit_references_precede_ambient_sources() {
  starmap_tests "./internal/auth" \
    TestExplicitCredentialReferencesPrecedeAmbientSources &&
    starport_tests "./internal/credentials ./internal/config" \
      TestExplicitCredentialReferencesPrecedeAmbientSources
}

source_conformance_vectors_match() {
  export CDP_SOURCE_CONFORMANCE_VECTORS
  CDP_SOURCE_CONFORMANCE_VECTORS='static,default_chain,version,expiry,lease,cancellation,concurrency,denial,redaction,rotation_in_place,rotation_atomic_replace,rotation_symlink_swap,rotation_mounted_replace,rotation_agent_rerender'

  starmap_tests "./internal/auth" TestCredentialSourceConformance &&
    starport_tests "./internal/credentials" TestCredentialSourceConformance
}

old_starport_environment_names_are_absent() {
  no_production_go_match \
    'STARPORT_PROVIDERS_[A-Z0-9_]+' \
    "$STARPORT_ROOT/cmd" \
    "$STARPORT_ROOT/internal"
}

starport_has_no_provider_roster() {
  starport_tests "./internal/architecture" \
    TestStarportProductionHasNoProviderRoster
}

starmap_has_no_acquisition_roster() {
  starmap_tests "./internal/providers/clients ./internal/auth" \
    TestStarmapAcquisitionHasNoProviderRoster
}

prohibited_provider_facts_are_absent() {
  local facts='(OPENAI_API_KEY|ANTHROPIC_API_KEY|GOOGLE_API_KEY|GROQ_API_KEY|MISTRAL_API_KEY|FIREWORKS_API_KEY|api\.openai\.com|api\.anthropic\.com|generativelanguage\.googleapis\.com|aiplatform\.googleapis\.com|api\.groq\.com|api\.mistral\.ai|claude-3-haiku|gemini-1\.5-flash|llama3-8b-8192|text-embedding-ada-002)'

  # Protocol codecs, provider-specific transport packages, tests, fixtures,
  # documentation, and declarative YAML are outside these shared scan zones.
  no_production_go_match "$facts" \
    "$STARPORT_ROOT/internal/config" \
    "$STARPORT_ROOT/internal/app" \
    "$STARPORT_ROOT/internal/setup" \
    "$STARPORT_ROOT/internal/diagnosis" \
    "$STARPORT_ROOT/internal/providers/keyring" \
    "$STARPORT_ROOT/internal/registry" &&
    no_production_go_match "$facts" \
      "$STARMAP_ROOT/internal/auth" \
      "$STARMAP_ROOT/internal/cli" \
      "$STARMAP_ROOT/internal/providers/clients/provider.go" \
      "$STARMAP_ROOT/internal/sources/providers"
}

registries_use_primitives_only() {
  starmap_tests "./internal/providers/clients ./internal/transport" \
    TestTransportAuthenticationRegistriesUsePrimitives &&
    starport_tests "./internal/architecture ./internal/registry" \
      TestTransportAuthenticationRegistriesUsePrimitives
}

connectors_are_credential_free() {
  starport_tests "./internal/providers/connectors ./internal/app" \
    TestConnectorsStoreNoCredentialMaterial \
    TestConcurrentRequestsUseOnlySelectedCredentialMaterial
}

synthetic_provider_inference_works() {
  starmap_tests "./internal/catalog/pipeline" \
    TestYAMLOnlyProviderAcquisitionPublishesReviewedOfferingAndQuarantinesUnknown &&
    starport_tests "./internal/app ./internal/proxy" \
      TestSyntheticCatalogProviderInferenceContract
}

synthetic_provider_operator_surfaces_work() {
  starport_tests "./internal/setup ./internal/diagnosis ./internal/providers/keyring" \
    TestSyntheticCatalogProviderOperatorSurfaces
}

unsupported_catalog_primitives_fail_closed() {
  starport_tests "./internal/catalog ./internal/app ./internal/failure" \
    TestUnsupportedCatalogPrimitivesRemainUnavailable
}

verified_remote_provider_activates() {
  starport_tests "./internal/catalog ./internal/app" \
    TestVerifiedRemoteCatalogActivatesProvider
}

runtime_generation_replacement_is_atomic() {
  starport_tests "./internal/catalog ./internal/app ./internal/registry" \
    TestRuntimeGenerationRejectsInvalidCandidates \
    TestInvalidRuntimeCandidateRetainsCacheIdentity \
    TestRuntimeGenerationDrainsConnectors
}

credential_strategy_order_is_exact() {
  starport_tests "./internal/providers/keyring ./internal/execution" \
    TestStrategyOrdersAllThreeSources
}

test -d "$STARPORT_ROOT/internal" || {
  printf 'Starport root is invalid: %s\n' "$STARPORT_ROOT" >&2
  exit 2
}
test -d "$STARMAP_ROOT/pkg/catalogs" || {
  printf 'Starmap root is invalid: %s\n' "$STARMAP_ROOT" >&2
  exit 2
}

run_check CDP-V01 'credential schema preserves one strict contract' \
  credential_contract
run_check CDP-V02 'acquisition and inference credential values stay isolated' \
  credential_planes_are_isolated
run_check CDP-V03 'every provider YAML record uses the credential schema' \
  provider_yaml_uses_credential_schema
run_check CDP-V04 'both products implement catalog-declared environment precedence' \
  environment_precedence
run_check CDP-V05 'alias collisions fail before reads or connector construction' \
  alias_collisions_fail_before_reads
run_check CDP-V06 'explicit references precede ambient sources and fail closed' \
  explicit_references_precede_ambient_sources
run_check CDP-V07 'both products pass the same source-conformance vectors' \
  source_conformance_vectors_match
run_check CDP-V08 'old Starport provider environment names are absent' \
  old_starport_environment_names_are_absent
run_check CDP-V09 'Starport production selection has no provider roster' \
  starport_has_no_provider_roster
run_check CDP-V10 'Starmap acquisition selection has no provider roster' \
  starmap_has_no_acquisition_roster
run_check CDP-V11 'shared production zones contain no provider or model facts' \
  prohibited_provider_facts_are_absent
run_check CDP-V12 'transport and authentication registries use primitives only' \
  registries_use_primitives_only
run_check CDP-V13 'connectors retain no credential and requests stay isolated' \
  connectors_are_credential_free
run_check CDP-V14 'synthetic provider inference and discovery use catalog facts' \
  synthetic_provider_inference_works
run_check CDP-V15 'synthetic provider operator surfaces use catalog facts' \
  synthetic_provider_operator_surfaces_work
run_check CDP-V16 'unsupported catalog declarations fail closed' \
  unsupported_catalog_primitives_fail_closed
run_check CDP-V17 'verified remote provider generations activate without rebuilds' \
  verified_remote_provider_activates
run_check CDP-V18 'runtime replacement is atomic and drains old connectors' \
  runtime_generation_replacement_is_atomic
run_check CDP-V19 'credential strategy source order is exact' \
  credential_strategy_order_is_exact

printf 'Summary: %d passed, %d failed\n' "$passed" "$failed"
test "$failed" -eq 0
