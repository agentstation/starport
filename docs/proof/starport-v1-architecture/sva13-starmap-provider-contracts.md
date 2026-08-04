# SVA13 Starmap Provider Contracts

Date: 2026-08-03

Status: `done`

## Outcome

Starmap now owns a typed provider contract for catalog-acquisition
authentication and inference service facts. The contract keeps credential
values out of catalog generations. Starport remains the owner of gateway and
provider inference authentication.

The embedded provider database now includes validated records for Mistral,
Azure OpenAI, and Ollama. Azure deployment names and Ollama local models are
not embedded because they are installation-specific observations.

## Fail-before evidence

The new contract tests first failed to compile because Starmap had no typed
catalog auth method, inference operation, service-capability, or workload
identity contract. Mistral, Azure OpenAI, and Ollama also had no embedded
provider records.

## Implemented seams

- `pkg/catalogs` owns catalog auth methods, inference operations, service
  endpoints, offering service capabilities, validation, and deep copies.
- `internal/auth` selects acquisition credential resolvers from catalog auth
  metadata. Endpoint type no longer selects credentials.
- Google provider acquisition accepts the SDK default credential chain. It no
  longer rejects workload identity or metadata credentials through a local ADC
  file preflight.
- The embedded provider database contains service, status, and acquisition
  facts for 14 providers.
- Generated endpoint projections use schema version 2 and contain per-operation
  service endpoints.
- Starmap documentation states the acquisition-auth and inference-auth
  boundary.

## Verification

- `TestCatalogAcquisitionAuthContract`,
  `TestAcquisitionCredentialsNeverSerialize`,
  `TestCloudCredentialChainSelection`,
  `TestBuildDetailsRecognizesWorkloadIdentity`,
  `TestProviderInferenceContract`,
  `TestProviderOfferingServiceCapabilities`, and
  `TestEmbeddedProviderContracts` pass.
- `go test ./... -count=1` passes.
- The affected acquisition, auth, provider, catalog, and embedded packages pass
  with the race detector.
- `go vet ./...` passes.
- `devbox run golangci-lint run` reports `0 issues` with the repository-pinned
  linter.
- `make generate` completes and `make docs-check` passes.
- `go run ./cmd/starmap validate catalog` validates 14 providers, 104 authors,
  611 model definitions, and all cross-references.
- The ownership verifier's O11 condition passes.
- `git diff --check` passes.

The plan named `make catalog-cross-reference-check`, but Starmap has no such
target. The repository-owned `starmap validate catalog` command checked all
cross-references and returned success.

I did not create a commit, branch, release, or publication.
