# DX6 cloud provider authentication proof

Date: 2026-08-09

## Fail-before state

The Vertex AI connector used one configured value as an access token for every
request. The Azure OpenAI connector used one configured API key for every
request. Neither connector could get or refresh a cloud credential.

This command captured the static contracts before implementation:

```bash
go test ./internal/providers/connectors \
  -run 'Test(VertexAIConnector_Chat|AzureOpenAIConnector_Chat|AdapterRegistryAppliesInferenceAuthentication)' \
  -count=1
```

The tests passed because they required `Bearer test-token` for Vertex AI and
`api-key: test-key` for Azure OpenAI. The developer-experience verifier failed
all three DX6 authentication conditions.

## Implementation

Implementation branch: `codex/starport-dx6-cloud-auth`.

- `internal/providerauth` owns renewable inference tokens and cloud SDK
  adapters.
- A refreshable source caches a token and replaces it two minutes before
  expiry.
- One request refreshes the credential. Other requests can stop through
  their own contexts.
- A token without a value, expiry, or sufficient remaining life fails closed.
- The bearer transport uses the inference request context. It changes a
  request copy and forwards idle-connection closure.
- Google default mode uses Application Default Credentials with the Google
  Cloud platform scope. It preserves the detected quota project for provider
  requests.
- Azure default mode uses `DefaultAzureCredential` with the Azure Cognitive
  Services scope.
- Each cloud adapter requires an explicit `AUTH_MODE`. Ambient cloud
  credentials do not activate either inference adapter.
- Static mode keeps the Starport provider secret path. Starport rejects an
  empty mode or default mode combined with an API key.
- Starmap catalog-acquisition credentials do not enter the new source or
  connector paths.

The implementation follows the official
[Google authentication package](https://pkg.go.dev/cloud.google.com/go/auth/credentials),
[Azure Identity package](https://pkg.go.dev/github.com/Azure/azure-sdk-for-go/sdk/azidentity),
and [Azure OpenAI authentication examples](https://learn.microsoft.com/en-us/azure/foundry/openai/supported-languages).

## Contract verification

Commands:

```bash
go test -race ./internal/providerauth ./internal/config \
  ./internal/providers ./internal/providers/connectors -count=1
bash scripts/verify-developer-experience.sh
```

Results:

```text
All four focused race packages passed.
Summary: 26 passed, 13 failed
```

All DX6 conditions pass. The 13 verifier failures belong to DX7 and DX8.

The tests cover these contracts:

- Early refresh and fresh-token reuse.
- One refresh under concurrent demand.
- Cancellation for a waiting request and for each cloud connector.
- Empty, missing-expiry, and stale token rejection.
- Request-copy authorization and idle-connection closure.
- Google and Azure SDK token adaptation.
- Google quota-project propagation without explicit-header replacement.
- The Azure Cognitive Services scope.
- Static and default mode validation.
- Explicit cloud inference opt-in.
- Independent Starmap and Starport credential planes.
- Vertex AI and Azure OpenAI token replacement before expiry.

## Repository gates

These commands passed:

```bash
bash scripts/verify-starmap-ownership.sh
bash scripts/verify-v1-architecture.sh
go test ./...
go vet ./...
make lint
make build
bash scripts/smoke-openrouter-sdks.sh
```

The ownership verifier passed 12 checks. The architecture verifier passed 12
checks. Lint reported zero issues. The SDK smoke suite passed raw chat,
streaming, model, and embedding requests. It also passed the Python,
TypeScript, and Go OpenRouter clients.

Strict technical-writing lint passed seven changed guides with zero diagnostics.
The glossary check reported 15 terms and zero errors.

## Review and pull request gate

The `sol` profile used `gpt-5.6-sol` at high reasoning. TruffleHog reported a
clean bundle. The wider review found two failure-path defects:

- Concurrent waiters retried one failed credential refresh in sequence.
- A credential failure did not close the outbound request body.

Starport now shares a failed refresh with its waiting cohort. A later request
can retry. A waiter also retries when the refresh leader cancels its own
context. The bearer transport closes the request body before it returns an
authentication error.

The first convergence review found a timing assumption in the new failed
cohort test. The test now verifies the cohort wait contract directly and tests
a later retry separately. Fifty repeated race runs passed.

The next convergence review found that the Google source discarded the quota
project from Application Default Credentials. The source now preserves that
value, and the bearer transport sends it as `X-Goog-User-Project`. This change
supports local credentials that charge requests to an explicit quota project.

The pre-pull-request review found that a general HTTP client could run the
credential transport again after a cross-origin redirect. This behavior could
send a renewable credential to the redirect target. The transport now rejects
all redirect requests before it reads or sends a credential.

The next step runs the convergence review and pull request gate.
