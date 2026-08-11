# APR3 credential-driven runtime refresh proof

Status: done  
Work commit: `3379a96`

## Result

APR3 adds one catalog-driven provider reconciliation boundary.

- Construction resolves catalog-declared process environment fields without
  contacting a remote secret store or cloud identity endpoint.
- `App.Run` starts one cancellable background reconciliation. A configurable
  interval repeats it. A manual application port forces source refresh and
  shares concurrent work. APR5 owns the authenticated HTTP route.
- Each provider has an independent timeout. One provider failure does not
  block another provider or gateway readiness.
- The Starmap inference profile selects a compiled cloud chain. The chain
  declares the exact fields that it can supply. Starport does not call it when
  a profile lacks another required field.
- Google default credentials can supply the catalog-declared project and quota
  project fields. Ambient catalog fields keep precedence.
- At request time, Starport reads operator material only from cache. It does not
  contact a secret store or cloud identity endpoint. The background reconciler
  refreshes the cache.
- A transient source failure retains valid material. A successful
  `not_configured` result removes stale material. Explicit revocation prevents
  an in-flight result from repopulating the cache.
- A token-only rotation updates the shared cache without replacing connectors.
  A provider, profile, endpoint-binding, or source-policy change publishes one
  complete runtime availability revision against the exact Starmap generation.
- Catalog publication and credential publication use one application lock.
  Starport rejects a credential result from an old catalog generation.

The change adds no provider roster, provider-specific alias table, module
dependency, compatibility path, or inference probe.

The one-minute reconcile interval and ten-second per-provider timeout are
configurable operational defaults. They are not acceptance budgets or adapter
admission rules. An interval of zero disables periodic work after the required
startup pass. A process environment change made outside the running Starport
process still requires restart.

## Fail-before evidence

The APR0 verifier reported both owning conditions as red:

```text
FAIL APR-V06 declared cloud chains run outside the inference hot path
missing test: TestProviderReconcilerDiscoversGoogleDefault
FAIL APR-V07 provider reconciliation publishes atomic generations
missing test: TestProviderReconcilerDiscoversAmbientKey
```

Before APR3, runtime publication occurred only when the Starmap generation
changed. Default cloud chains required an unrelated observed field or explicit
provider setting. Google ADC projected a bearer token but not the Starmap
`project_field`. Provider resolution stopped at the first provider error, and
request-time material resolution could refresh an external source.

## Acceptance evidence

The campaign verifier returned the expected APR3 state:

```text
PASS APR-V01 provider-neutral bootstrap persists no provider credential
PASS APR-V02 local development uses loopback and in-memory storage
PASS APR-V03 catalog providers register without operator material
PASS APR-V04 request policy precedes endpoint binding and authentication
PASS APR-V05 Starmap inference profiles and order drive resolution
PASS APR-V06 declared cloud chains run outside the inference hot path
PASS APR-V07 provider reconciliation publishes atomic generations
PASS APR-V10 gateway readiness ignores provider credential availability
Summary: 8 passed, 4 failed
```

APR-V08, APR-V09, APR-V11, and APR-V12 remain assigned to later tasks. The
verifier exited with status `1` because those planned assertions remain red.

These named tests passed:

```text
TestProviderReconcilerDiscoversGoogleDefault
TestGoogleDefaultProjectsCatalogProjectField
TestProviderReconcilerSkipsUndeclaredCloudChain
TestDefaultChainWaitsForRequiredNonChainField
TestProviderReconcilerDiscoversAmbientKey
TestProviderReconcilerIntervalPublishesChangedGeneration
TestProviderReconcilerManualRefreshSharesInflight
TestProviderFailureDoesNotBlockOthers
TestProviderReconcilerCancellationStops
TestRuntimeGenerationDrainsConnectors
TestCachedProviderSourceNeverReadsBackend
TestLocalProviderResolutionSkipsRemoteSource
TestCredentialRefreshRevokesDisappearedMaterial
```

Existing lifecycle tests also passed for transient-failure retention,
revocation during in-flight work, leader cancellation, direct secret renewal,
and expiry. Rotation tests passed for file changes, atomic replacement, symlink
swaps, mounted-content replacement, and agent rerender.

## Verification evidence

The following checks passed with normal Go scheduling:

```text
go test ./...                                      41 packages, 35 with tests
go vet ./...                                       pass
make lint                                          0 issues
make build                                         pass
go test -race ./internal/credentials \
  ./internal/providers ./internal/registry \
  ./internal/app                                   4 packages
go test ./internal/credentials -run '^$' \
  -bench '^BenchmarkProviderCachedMaterial$' \
  -benchtime=10000x -count=1                       65.30 ns/op
bash scripts/verify-starmap-ownership.sh           12 passed, 0 failed
bash scripts/verify-v1-architecture.sh             12 passed, 0 failed
bash scripts/smoke-openrouter-sdks.sh              4 raw and 3 SDK checks
git diff --check                                   pass
```

The first race run identified concurrent lazy resolver construction in test
configurations that bypassed the loader. The fix puts synchronized resolver
initialization at the configuration owner. The next uncapped race run passed.
No command used `GOFLAGS=-p` or another scheduler cap.

The default autoreview selected Claude Opus 5 high. Its harness stopped before
token use because the local proxy supplied a self-signed certificate. The
secret scan passed. The Sol profile then reviewed the exact `3379a96` branch
diff with GPT-5.6-sol high and reported no accepted or actionable findings. Its
overall correctness confidence was `0.98`.
