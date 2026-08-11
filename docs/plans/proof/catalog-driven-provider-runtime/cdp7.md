# CDP7 Request credential policy and operator surfaces

Status: `done`

Starport work commit: `7e9fd41`

Starmap work commit: `a80da545`

## Fail-before evidence

- Inference routing did not apply the stored BYOK strategy to each provider
  attempt.
- Chat and embeddings did not share one credential-selection and attempt-budget
  contract.
- Setup accepted a compiled OpenAI and Ollama roster instead of the active
  Starmap catalog.
- Starmap authentication hints named `OPENAI_API_KEY` and the nonexistent
  `providers auth` command in compiled Go code.

## Request credential contract

Starport now accepts only these exact strategies:

| Strategy | Credential order |
|---|---|
| `operator_first` | Deployment-owned material, then tenant material. |
| `user_first` | Tenant material, then deployment-owned material. |
| `user_only` | Tenant material only. |

The default is `operator_first`. Each tenant lookup uses the exact authenticated
tenant scope. No request merges tenant records with a global record.
`user_only` does not resolve, read, or test operator material. The safe external
error has the same kind and message when operator material exists and when it
does not exist.

Not-configured, authentication, and rate-limit results can select the next
declared credential source. Permission, invalid material, timeout,
cancellation, and internal errors stop credential fallback. These errors use
typed, secret-free external messages.

## Execution and availability

Chat, streaming chat, and embeddings use the same immutable route plan,
request credential policy, total attempt budget, availability state, and
tenant restrictions. Credential-source continuation uses the same route and
consumes the existing attempt budget. Exhausted eligible credential sources
advance directly to the next planned route.

Credential resolution does not change provider-health state. A same-route
continuation retains the existing half-open admission. If no provider request
proves an outcome, execution releases the admission without success or failure.
This prevents credential failures from opening a provider circuit or consuming
a second half-open probe.

The response-cache semantic identity is version 2 and includes the tenant
credential strategy. A cache entry from one strategy cannot satisfy a request
that uses another strategy.

## Catalog-driven operator surfaces

Setup, diagnosis, BYOK validation, and the CLI now use the active catalog
credential contract. The synthetic `acme` provider proves that these surfaces
need no provider ID constant or provider branch. Unsupported transport or
authentication primitives fail closed with typed errors.

Setup checks conventional catalog environment names first. It then checks the
catalog-derived product alias `STARPORT_<PROVIDER>_<FIELD>`. Documentation and
`.env.example` no longer publish a static provider roster.

Starmap authentication hints now derive provider names and credential
environment variables from the catalog. The hint uses the real commands
`starmap providers` and `starmap providers --test`. The removed
`starmap providers auth` path cannot return.

## Contract tests

The CDP7 verifier assertions are green:

- `TestSyntheticCatalogProviderOperatorSurfaces` covers setup, diagnosis, and
  BYOK repository behavior for `acme`.
- `TestUnsupportedCatalogPrimitivesFailClosed` covers unsupported catalog
  primitives.
- `TestBYOKStrategyOrderAndUserOnlyNoninterference` covers exact strategy
  order, terminal errors, and the stable `user_only` external result.
- `TestUserOnlyCredentialPolicyDoesNotProbeOperatorMaterial` proves zero
  operator resolution calls.
- `TestCredentialResolutionTerminalFailureStopsWithoutProviderHealth` proves
  terminal credential behavior.
- `TestRouteEmbeddingsUsesRequestCredentialPolicy` and
  `TestRouteEmbeddingsUserOnlyNeverProbesOperatorMaterial` cover embeddings.
- `TestAuthenticationHintContainsNoProviderCredentialFact` protects the
  Starmap CLI boundary.

## Verification

The following Starport checks passed after the final source change. No command
used `GOFLAGS`, `-p`, or another scheduler cap.

- `go test ./... -count=1`: 41 packages passed.
- `go vet ./...`.
- `make lint`: zero issues.
- `make build`.
- `bash scripts/verify-starmap-ownership.sh`: 12 passed, 0 failed.
- `bash scripts/verify-v1-architecture.sh`: 12 passed, 0 failed.
- `bash scripts/smoke-first-run.sh`.
- `bash scripts/smoke-openrouter-sdks.sh`: raw HTTP chat, stream, models, and
  embeddings passed. Python, TypeScript, and Go OpenRouter SDKs passed.
- Strict technical-writing checks: four files passed with zero diagnostics.
- `git diff --check`.

This focused race command passed for all 11 named packages:

```text
go test -race ./internal/availability ./internal/execution ./internal/providers/byok ./internal/router ./internal/proxy ./internal/setup ./internal/diagnosis ./internal/cli ./internal/app ./internal/server ./internal/server/controllers
```

Starmap `make verify` passed across 85 packages. It included the uncapped race
suite, the pure-Go consumer matrix, and the file-size gate. It also included 15
coverage gates, vet, zero lint issues, documentation checks, and the binary
build. Generated catalog validation passed for 14 providers, 104 authors, and
611 models. CLI smoke checks also passed.

The final campaign verifier reported:

```text
Summary: 18 passed, 1 failed
```

CDP-V01 through CDP-V16 and CDP-V18 through CDP-V19 are green. CDP-V17 is the
only failure because `TestVerifiedRemoteCatalogActivatesProvider` does not yet
exist. CDP8 owns that test and remote activation contract. It is not a CDP7
failure.
