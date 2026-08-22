# CP2 — extract the catalog projection seam

## Changes

- Added `internal/catalog/view`: the package owns the console- and
  API-facing catalog projections. `view.go` holds the DTO types
  (`ModelInfo`, `ModelPricing`, `ModelArchitecture`, `TopProviderInfo`,
  `ProviderInfo`, `CredentialFieldInfo`, `EndpointInfo`), `models.go`,
  `providers.go`, and `endpoints.go` hold the moved-verbatim assembly.
- `view.Providers` takes `requiresAuth func(providerID string) bool`, so
  the view package never imports connectors. The proxy injects
  `runtime.RequiresAuthentication`.
- `internal/proxy/service.go` aliases the moved types
  (`type ModelInfo = view.ModelInfo`, …), so `internal/server` compiles
  unchanged. `internal/proxy/proxy.go` is now a thin caller.
  `cacheTokenPrices`/`modelTokenPrice` stay in proxy: they price cache
  hits, not catalog projection.
- Added `internal/proxy/projection_golden_test.go` plus three
  `testdata/projection_*.golden.json` fixtures in a pre-refactor commit
  (657556a), so the extraction proves byte identity against the old
  assembly.
- Added `internal/catalog/view/view_test.go`: nil-snapshot contracts,
  `formatTokenPrice` nil/PerToken/Per1M cases (the nil case relocated
  from `internal/proxy/catalog_facts_test.go`), `boundedModelInt` clamp,
  feature-to-parameter mapping, `requiresAuth` injection with nil guard,
  sorted capabilities, and unknown-model endpoints returning an empty
  non-nil slice.
- Tightened verifier conditions CPV03/CPV04: the old greps
  (`offerings`, `Description`) matched the moved code and test fixtures
  spuriously. CPV03 now requires the `"offerings"` JSON tag; CPV04 now
  requires `provider.Description` to be read in `providers.go`. Both
  correctly stay red until CP3.
- Recorded the CP17 live repro
  (`research/cp17-streaming-429-repro.md`): groq streams a 429 as an SSE
  `event: error` frame inside an HTTP 200, and the gateway stream codec
  drops it, producing an empty 200 that can be cached.

## Fail-before evidence

Before the move, the projection assembly was unexported inside
`internal/proxy` with no seam a unit test could target; the golden
baseline commit (657556a) is the pre-refactor pin the extraction is
proven against.

## Evidence

- `go test ./internal/catalog/... ./internal/proxy/`: ok, including the
  three golden tests against unchanged golden files (byte identity).
- `go test ./...`: ok. `go vet ./...`: clean. `make lint`: 0 issues.
  `make build`: complete.
- `bash scripts/verify-dependency-direction.sh`: 6 passed, 0 failed.
- `bash scripts/verify-starmap-ownership.sh`: 12 passed.
  `verify-v1-architecture.sh`: 12 passed. `verify-package-layout.sh`:
  passed.
- `bash scripts/verify-catalog-performance.sh`: 4 passed (CPV02,
  CPV15–CPV17), 14 failed as scoped — CP2 flips exactly CPV02.
