#!/bin/sh

set -u

passed=0
failed=0

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
control_root=$(CDPATH='' cd -- "$script_dir/../../../.." && pwd)
STARPORT_ROOT=${STARPORT_ROOT:-$control_root}
STARMAP_ROOT=${STARMAP_ROOT:-$(dirname "$STARPORT_ROOT")/starmap}

require_module() {
    root=$1
    module=$2
    test -d "$root" || return 1
    test -f "$root/go.mod" || return 1
    grep -q "^module $module$" "$root/go.mod"
}

if ! require_module "$STARPORT_ROOT" github.com/agentstation/starport; then
    printf 'ERROR: STARPORT_ROOT is not a Starport checkout: %s\n' "$STARPORT_ROOT" >&2
    exit 2
fi

if ! require_module "$STARMAP_ROOT" github.com/agentstation/starmap; then
    printf 'ERROR: STARMAP_ROOT is not a Starmap checkout: %s\n' "$STARMAP_ROOT" >&2
    exit 2
fi

check() {
    id=$1
    name=$2
    shift 2

    if output=$("$@" 2>&1); then
        passed=$((passed + 1))
        printf 'PASS %s %s\n' "$id" "$name"
    else
        failed=$((failed + 1))
        printf 'FAIL %s %s\n' "$id" "$name"
        if [ -n "$output" ]; then
            printf '%s\n' "$output" >&2
        fi
    fi
}

verify_starmap_layout() (
    set -e
    cd "$STARMAP_ROOT" || exit 1
    test -f scripts/verify-package-layout.sh
    bash scripts/verify-package-layout.sh
)

verify_starmap_behavior() (
    set -e
    cd "$STARMAP_ROOT" || exit 1
    test -d internal/bootstrap/manifest
    test -d internal/bootstrap/budget
    test -d internal/sources/payload
    test -d internal/test/catalog
    test -d internal/test/logging
    go test -count=1 ./internal/bootstrap/... ./internal/sources/... ./internal/test/... \
        ./cmd/starmap-bootstrap-manifest ./cmd/starmap-embedded-budget
)

verify_starmap_budget_policy() (
    set -e
    cd "$STARMAP_ROOT" || exit 1
    grep -R -q '^func TestCatalogBudgetPolicyClassification' internal/bootstrap/budget
    grep -R -q '^func TestCatalogBudgetReviewThresholdDoesNotReject' internal/bootstrap/budget
    grep -R -q '^func TestCatalogBudgetHardGateRequiresPolicy' internal/bootstrap/budget
    go test -count=1 ./internal/bootstrap/budget ./cmd/starmap-embedded-budget \
        -run '^(TestCatalogBudgetPolicyClassification|TestCatalogBudgetReviewThresholdDoesNotReject|TestCatalogBudgetHardGateRequiresPolicy)$'
)

verify_starmap_provider_fixtures() (
    set -e
    cd "$STARMAP_ROOT" || exit 1
    grep -R -q '^func TestOpenAICompatibleProviderCatalogContracts' internal/providers/openai
    grep -R -q '^func TestProviderFixture' internal/test/providerfixture
    test -f scripts/test-provider-testdata-refresh.sh
    go test -count=1 ./internal/providers/openai ./internal/test/providerfixture \
        -run '^(TestOpenAICompatibleProviderCatalogContracts|TestProviderFixture)'
    bash scripts/test-provider-testdata-refresh.sh
)

verify_starport_protocol_layout() (
    set -e
    cd "$STARPORT_ROOT" || exit 1
    test -f scripts/verify-package-layout.sh
    bash scripts/verify-package-layout.sh
    go test -count=1 ./internal/protocol/... ./internal/repotest ./internal/storage \
        ./internal/architecture ./internal/server
)

verify_starport_state_and_cache() (
    set -e
    cd "$STARPORT_ROOT" || exit 1
    test -d internal/providers/state
    test -d internal/response/cache
    go test -count=1 ./internal/providers/state ./internal/response/cache ./internal/proxy \
        ./internal/server ./internal/app ./internal/architecture
)

verify_starport_authentication_owners() (
    set -e
    cd "$STARPORT_ROOT" || exit 1
    grep -R -q '^func TestProviderAuthenticationPackageHasNoCloudSDKImports' internal/architecture
    grep -R -q '^func TestCloudChainPackageDoesNotMutateHTTPRequests' internal/architecture
    go test -count=1 ./internal/providers/auth ./internal/credentials/cloudchain \
        ./internal/credentials ./internal/providers/connectors ./internal/architecture
)

verify_starport_http_transport() (
    set -e
    cd "$STARPORT_ROOT" || exit 1
    test ! -e internal/httpclient
    if grep -q '^[[:space:]]*golang.org/x/time' go.mod; then
        exit 1
    fi
    grep -R -q '^func TestProviderHTTPTransportContract' internal/providers/connectors
    grep -R -q '^func TestProviderHTTPClientHasNoTotalTimeout' internal/providers/connectors
    grep -R -q '^func TestProviderTransportDoesNotMutateResponseHeaders' internal/providers/connectors
    grep -R -q '^func TestProductionConnectorCallsUseExecutionDeadline' internal/architecture
    go test -count=1 ./internal/providers/connectors ./internal/execution \
        ./internal/router ./internal/architecture
)

verify_closeout() (
    set -e
    test -f "$script_dir/por8.md"
    grep -q '^POR8_TERMINAL: PASS$' "$script_dir/por8.md"
    test "$(git -C "$STARPORT_ROOT" branch --show-current)" = main
    test "$(git -C "$STARMAP_ROOT" branch --show-current)" = main
    test -z "$(git -C "$STARPORT_ROOT" status --porcelain=v1)"
    test -z "$(git -C "$STARMAP_ROOT" status --porcelain=v1)"
)

check POR-V01 'Starmap approved package layout' verify_starmap_layout
check POR-V02 'Starmap source-payload and bootstrap behavior' verify_starmap_behavior
check POR-V03 'Starmap catalog limit policy' verify_starmap_budget_policy
check POR-V04 'Starmap YAML-backed provider fixtures' verify_starmap_provider_fixtures
check POR-V05 'Starport protocol and test-support layout' verify_starport_protocol_layout
check POR-V06 'Starport provider state and response cache' verify_starport_state_and_cache
check POR-V07 'Starport authentication owner separation' verify_starport_authentication_owners
check POR-V08 'Starport connector-owned HTTP transport' verify_starport_http_transport
check POR-V09 'cross-repository closeout' verify_closeout

printf 'Summary: %d passed, %d failed\n' "$passed" "$failed"

if [ "$failed" -ne 0 ]; then
    exit 1
fi
