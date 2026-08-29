#!/bin/sh

set -u

passed=0
failed=0

check() {
    id="$1"
    name="$2"
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

# Go templates and awk programs must remain literal inside the nested shell.
# shellcheck disable=SC2016
check V01 'published Starmap module boundary and Go floor' sh -c '
    awk '\''$1 == "go" { split($2, version, "."); exit !(version[1] > 1 || version[2] >= 25) }'\'' go.mod &&
	module_version=$(go list -m -f '\''{{if .Replace}}replace{{else}}{{.Version}}{{end}}'\'' github.com/agentstation/starmap) &&
	printf "%s\n" "$module_version" | grep -Eq '\''^v[0-9]+\.[0-9]+\.[0-9]+$'\'' &&
	scripts/require-no-match.sh grep -Eq '\''^[[:space:]]*replace[[:space:]]+github.com/agentstation/starmap([[:space:]]|$)'\'' go.mod &&
	scripts/require-no-match.sh grep -R -q -E --include="*.go" '\''"github.com/agentstation/starmap/pkg/(catalogartifact|catalogmeta|catalogremote|catalogstore)"'\'' .
'

check V02 'canonical inference contract' sh -c '
    grep -R -q -E --include="*_test.go" "^func TestCanonicalInferenceContract" internal/inference &&
    go test ./internal/inference -run "^TestCanonicalInferenceContract$"
'

check V03 'routable snapshot generation contract' sh -c '
    grep -R -q -E --include="*_test.go" "^func TestRoutableSnapshotGenerationConsistency" internal/catalog &&
    go test ./internal/catalog -run "^TestRoutableSnapshotGenerationConsistency$"
'

check V04 'deterministic route planner contract' sh -c '
    grep -R -q -E --include="*_test.go" "^func TestRoutePlannerContract" internal/routing &&
    grep -R -q -E --include="*_test.go" "^func TestRoutePlannerDeterministic" internal/routing &&
    go test ./internal/routing -run "^TestRoutePlanner(Contract|Deterministic)$"
'

check V05 'attempt state and retry budget contract' sh -c '
    grep -R -q -E --include="*_test.go" "^func TestAttemptStateAndRetryBudgetContract" internal/execution &&
    grep -R -q -E --include="*_test.go" "^func TestProviderHTTPRequestIsOneLogicalAttempt" internal/providers/connectors &&
    scripts/require-no-match.sh grep -R -q -E --include="*.go" "doRequestWithRetry|WithRetry|CircuitBreaker|fallbackLocations|fallback_locations" internal/providers/connectors internal/router &&
    go test ./internal/execution ./internal/availability -run "^(TestAttemptStateAndRetryBudgetContract|TestOfferingAvailabilityStateMachine)$"
'

check V06 'versioned concept repository contracts' sh -c '
	scripts/require-no-match.sh grep -R -q -E --include="*.go" '\''"(apikey:|apikey:hash:|ratelimit:|preset:|providerkey:|provider_key:)"'\'' internal &&
	grep -q -E "StorageSchemaVersion = 1" internal/identity/repository.go &&
	grep -q -E "ProviderCredentialStorageSchemaVersion = 1" internal/credentials/repository.go &&
	grep -q -E "StorageSchemaVersion = 1" internal/ratelimit/repository.go &&
	grep -q -E "StorageSchemaVersion = 1" internal/presets/repository.go &&
	grep -R -q -E --include="*_test.go" "^func TestIdentityRepositoryContract" internal/identity &&
	grep -R -q -E --include="*_test.go" "^func TestProviderCredentialRepositoryContract" internal/credentials &&
	grep -R -q -E --include="*_test.go" "^func TestRateLimitRepositoryContract" internal/ratelimit &&
	grep -R -q -E --include="*_test.go" "^func TestPresetRepositoryContract" internal/presets &&
	scripts/require-no-match.sh grep -R -q -E --include="*.go" --exclude="*_test.go" "internal/storage" internal/server/controllers internal/server/middleware.go internal/console internal/providers/keyring &&
	go test ./internal/identity ./internal/credentials ./internal/ratelimit ./internal/presets -run "^Test(Identity|ProviderCredential|RateLimit|Preset)RepositoryContract$"
'

check V07 'response cache semantic identity contract' sh -c '
    grep -R -q -E --include="*_test.go" "^func TestSemanticKeyAndAccountIsolationContract" internal/response/cache &&
    grep -R -q -E --include="*_test.go" "^func TestCachedService_AccountAndGenerationIsolation" internal/proxy &&
    grep -q -E "RecordSchemaVersion = 1" internal/response/cache/repository.go &&
    test ! -e internal/cache/keys.go &&
    scripts/require-no-match.sh grep -R -q -E --include="*.go" "KeyGenerator|ChatCompletionKey|GetChatCompletion|SetChatCompletion|GetEmbedding|SetEmbedding" internal/cache &&
    scripts/require-no-match.sh grep -R -q -E --include="*.go" "internal/(cache|providers|proxy|server)" internal/response/cache &&
    scripts/require-no-match.sh grep -q -E "sha256|sha\.Sum|hex\.Encode" internal/proxy/cache.go &&
    go test ./internal/response/cache -run "^(TestSemanticKeyAndAccountIsolationContract|TestCanonicalRecordAndStreamReconstruction|TestRepositoryRejectsInvalidRecords)$" &&
    go test ./internal/proxy -run "^(TestCachedService_AccountAndGenerationIsolation|TestCachingStreamWrapper_DoesNotCachePartialStream)$"
'

check V08 'production composition fail-closed contract' sh -c '
    grep -R -q -E --include="*_test.go" "^func TestProductionCompositionFailsClosed" internal/app &&
	grep -R -q -E --include="*_test.go" "^func TestRuntimeRequiresNamedIdentity" internal/app &&
	grep -q -E "^type Dependencies struct" internal/server/server.go &&
	grep -q -E "^func Open" internal/registry/registry.go &&
	grep -q -E "^func .*Registry.* Start" internal/registry/registry.go &&
    scripts/require-no-match.sh grep -R -q -E --include="*.go" --exclude="*_test.go" "router\\.New|proxy\\.New|byok\\.NewProviderKeys|storage\\.NewMockStore" internal/server &&
	scripts/require-no-match.sh grep -R -q -E --include="*.go" --exclude="*_test.go" "internal/(storage|registry|cache|router)" internal/server &&
	scripts/require-no-match.sh grep -R -q -E --include="*.go" --exclude="*_test.go" "NewMockConnector|NewMockStore" internal/app internal/registry internal/server &&
    scripts/require-no-match.sh grep -R -q -E --include="*.go" --exclude="*_test.go" "os\\.Getenv|initializeMockProvider" internal/registry &&
    go test ./internal/app -run "^Test(ProductionCompositionFailsClosed|RuntimeRequiresNamedIdentity)$"
'

check V09 'public package boundary contract' sh -c '
    grep -R -q -E --include="*_test.go" "^func TestPublicPackageBoundary" internal/architecture &&
    go test ./internal/architecture -run "^TestPublicPackageBoundary$"
'

check V10 'OpenAI and OpenRouter protocol contracts' sh -c '
    grep -R -q -E --include="*_test.go" "^func TestOpenAIProtocolContract" internal/protocol/openai &&
    grep -R -q -E --include="*_test.go" "^func TestOpenRouterProtocolContract" internal/protocol/openrouter &&
    grep -R -q -E --include="*_test.go" "^func TestProtocolRoutesUseSelectedCodec" internal/server &&
	grep -R -q -E --include="*_test.go" "^func TestClientIPIgnoresUntrustedForwardingHeaders" internal/server &&
	scripts/require-no-match.sh grep -R -q -E --include="*.go" "middleware\.RealIP" internal/server &&
    go test ./internal/protocol/openai -run "^TestOpenAIProtocolContract$" &&
    go test ./internal/protocol/openrouter -run "^TestOpenRouterProtocolContract$" &&
    go test ./internal/server -run "^Test(ProtocolRoutesUseSelectedCodec|ClientIPIgnoresUntrustedForwardingHeaders)$"
'

check V11 'import graph architecture fitness' sh -c '
    grep -R -q -E --include="*_test.go" "^func TestImportGraphArchitecture" internal/architecture &&
    grep -R -q -E --include="*_test.go" "^func TestApprovedInternalPackageLayout" internal/architecture &&
    grep -R -q -E --include="*_test.go" "^func TestProviderAuthenticationPackageHasNoCloudSDKImports" internal/architecture &&
    grep -R -q -E --include="*_test.go" "^func TestCloudChainPackageDoesNotMutateHTTPRequests" internal/architecture &&
    grep -R -q -E --include="*_test.go" "^func TestProductionConnectorCallsUseExecutionDeadline" internal/architecture &&
    bash scripts/test-dependency-direction-verifier.sh &&
    bash scripts/verify-dependency-direction.sh &&
    go test ./internal/architecture -run "^Test(ImportGraphArchitecture|ApprovedInternalPackageLayout|ProviderAuthenticationPackageHasNoCloudSDKImports|CloudChainPackageDoesNotMutateHTTPRequests|ProductionConnectorCallsUseExecutionDeadline)$" &&
    bash scripts/verify-package-layout.sh &&
    bash scripts/test-package-layout-verifier.sh
'

check V12 'full Go test suite' go test ./...

printf 'Summary: %d passed, %d failed\n' "$passed" "$failed"

if [ "$failed" -ne 0 ]; then
    exit 1
fi
