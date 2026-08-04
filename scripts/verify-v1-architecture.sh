#!/bin/sh

set -u

passed=0
failed=0

check() {
    id="$1"
    name="$2"
    shift 2

    if "$@" >/dev/null 2>&1; then
        passed=$((passed + 1))
        printf 'PASS %s %s\n' "$id" "$name"
    else
        failed=$((failed + 1))
        printf 'FAIL %s %s\n' "$id" "$name"
    fi
}

check V01 'Starmap module and Go floor' sh -c '
    awk '\''$1 == "go" { split($2, version, "."); exit !(version[1] > 1 || version[2] >= 25) }'\'' go.mod &&
	module_version=$(go list -m -f '\''{{if .Replace}}replace{{else}}{{.Version}}{{end}}'\'' github.com/agentstation/starmap) &&
	printf "%s\n" "$module_version" | grep -Eq '\''^v[0-9]+\.[0-9]+\.[0-9]+$'\'' &&
	scripts/require-no-match.sh grep -Eq '\''^[[:space:]]*replace[[:space:]]+github.com/agentstation/starmap([[:space:]]|$)'\'' go.mod
'

check V02 'canonical inference contract' sh -c '
    rg -q "^func TestCanonicalInferenceContract" internal/inference --glob "*_test.go" &&
    go test ./internal/inference -run "^TestCanonicalInferenceContract$"
'

check V03 'routable snapshot generation contract' sh -c '
    rg -q "^func TestRoutableSnapshotGenerationConsistency" internal/catalog --glob "*_test.go" &&
    go test ./internal/catalog -run "^TestRoutableSnapshotGenerationConsistency$"
'

check V04 'deterministic route planner contract' sh -c '
    rg -q "^func TestRoutePlannerContract" internal/routing --glob "*_test.go" &&
    rg -q "^func TestRoutePlannerDeterministic" internal/routing --glob "*_test.go" &&
    go test ./internal/routing -run "^TestRoutePlanner(Contract|Deterministic)$"
'

check V05 'attempt state and retry budget contract' sh -c '
    rg -q "^func TestAttemptStateAndRetryBudgetContract" internal/execution --glob "*_test.go" &&
    rg -q "^func TestProviderHTTPRequestIsOneLogicalAttempt" internal/providers/connectors --glob "*_test.go" &&
    scripts/require-no-match.sh rg -q "doRequestWithRetry|WithRetry|CircuitBreaker|fallbackLocations|fallback_locations" internal/providers/connectors internal/router internal/httpclient --glob "*.go" &&
    go test ./internal/execution ./internal/availability -run "^(TestAttemptStateAndRetryBudgetContract|TestOfferingAvailabilityStateMachine)$"
'

check V06 'versioned concept repository contracts' sh -c '
	scripts/require-no-match.sh rg -q '\''"(apikey:|apikey:hash:|ratelimit:|preset:|providerkey:|provider_key:)"'\'' internal --glob "*.go" &&
	rg -q "StorageSchemaVersion = 1" internal/identity/repository.go &&
	rg -q "ProviderCredentialStorageSchemaVersion = 1" internal/credentials/repository.go &&
	rg -q "StorageSchemaVersion = 1" internal/ratelimit/repository.go &&
	rg -q "StorageSchemaVersion = 1" internal/presets/repository.go &&
	rg -q "^func TestIdentityRepositoryContract" internal/identity --glob "*_test.go" &&
	rg -q "^func TestProviderCredentialRepositoryContract" internal/credentials --glob "*_test.go" &&
	rg -q "^func TestRateLimitRepositoryContract" internal/ratelimit --glob "*_test.go" &&
	rg -q "^func TestPresetRepositoryContract" internal/presets --glob "*_test.go" &&
	scripts/require-no-match.sh rg -q "internal/storage" internal/server/controllers internal/server/middleware.go internal/chatui internal/providers/byok --glob "*.go" --glob "!*_test.go" &&
	go test ./internal/identity ./internal/credentials ./internal/ratelimit ./internal/presets -run "^Test(Identity|ProviderCredential|RateLimit|Preset)RepositoryContract$"
'

check V07 'response cache semantic identity contract' sh -c '
    rg -q "^func TestSemanticKeyAndTenantIsolationContract" internal/responsecache --glob "*_test.go" &&
    rg -q "^func TestCachedService_TenantAndGenerationIsolation" internal/proxy --glob "*_test.go" &&
    rg -q "RecordSchemaVersion = 1" internal/responsecache/repository.go &&
    test ! -e internal/cache/keys.go &&
    scripts/require-no-match.sh rg -q "KeyGenerator|ChatCompletionKey|GetChatCompletion|SetChatCompletion|GetEmbedding|SetEmbedding" internal/cache --glob "*.go" &&
    scripts/require-no-match.sh rg -q "internal/(cache|providers|proxy|server)" internal/responsecache --glob "*.go" &&
    scripts/require-no-match.sh rg -q "sha256|sha\.Sum|hex\.Encode" internal/proxy/cache.go &&
    go test ./internal/responsecache -run "^(TestSemanticKeyAndTenantIsolationContract|TestCanonicalRecordAndStreamReconstruction|TestRepositoryRejectsInvalidRecords)$" &&
    go test ./internal/proxy -run "^(TestCachedService_TenantAndGenerationIsolation|TestCachingStreamWrapper_DoesNotCachePartialStream)$"
'

check V08 'production composition fail-closed contract' sh -c '
    rg -q "^func TestProductionCompositionFailsClosed" internal/app --glob "*_test.go" &&
	rg -q "^func TestBootstrapIdentityContract" internal/app --glob "*_test.go" &&
	rg -q "^type Dependencies struct" internal/server/server.go &&
	rg -q "^func Open" internal/registry/registry.go &&
	rg -q "^func .*Registry.* Start" internal/registry/registry.go &&
    scripts/require-no-match.sh rg -q "router\\.New|proxy\\.New|byok\\.NewProviderKeys|storage\\.NewMockStore" internal/server --glob "*.go" --glob "!*_test.go" &&
	scripts/require-no-match.sh rg -q "internal/(storage|registry|cache|router)" internal/server --glob "*.go" --glob "!*_test.go" &&
	scripts/require-no-match.sh rg -q "NewMockConnector|NewMockStore" internal/app internal/registry internal/server --glob "*.go" --glob "!*_test.go" &&
    scripts/require-no-match.sh rg -q "os\\.Getenv|initializeMockProvider" internal/registry --glob "*.go" --glob "!*_test.go" &&
    go test ./internal/app -run "^Test(ProductionCompositionFailsClosed|BootstrapIdentityContract)$"
'

check V09 'public package boundary contract' sh -c '
    rg -q "^func TestPublicPackageBoundary" internal/architecture --glob "*_test.go" &&
    go test ./internal/architecture -run "^TestPublicPackageBoundary$"
'

check V10 'OpenAI and OpenRouter protocol contracts' sh -c '
    rg -q "^func TestOpenAIProtocolContract" internal/httpapi/openai --glob "*_test.go" &&
    rg -q "^func TestOpenRouterProtocolContract" internal/httpapi/openrouter --glob "*_test.go" &&
    rg -q "^func TestProtocolRoutesUseSelectedCodec" internal/server --glob "*_test.go" &&
	rg -q "^func TestClientIPIgnoresUntrustedForwardingHeaders" internal/server --glob "*_test.go" &&
	scripts/require-no-match.sh rg -q "middleware\.RealIP" internal/server --glob "*.go" &&
    go test ./internal/httpapi/openai -run "^TestOpenAIProtocolContract$" &&
    go test ./internal/httpapi/openrouter -run "^TestOpenRouterProtocolContract$" &&
    go test ./internal/server -run "^Test(ProtocolRoutesUseSelectedCodec|ClientIPIgnoresUntrustedForwardingHeaders)$"
'

check V11 'import graph architecture fitness' sh -c '
    rg -q "^func TestImportGraphArchitecture" internal/architecture --glob "*_test.go" &&
    go test ./internal/architecture -run "^TestImportGraphArchitecture$"
'

check V12 'full Go test suite' go test ./...

printf 'Summary: %d passed, %d failed\n' "$passed" "$failed"

if [ "$failed" -ne 0 ]; then
    exit 1
fi
