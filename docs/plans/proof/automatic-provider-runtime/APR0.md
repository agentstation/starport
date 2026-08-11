# APR0 baseline and verifier proof

Status: done  
Baseline: `b52cd7e286a9a870293155638392ed514b630a47`  
Plan merge: `f951817b18a86217b7c6349f4a62904cf5561f3a`  
Work commit: `21d2579`

## Result

APR0 added `scripts/verify-automatic-provider-runtime.sh` without changing
production behavior. The verifier owns APR-V01 through APR-V12, prints every
result, and exits nonzero while any assertion fails.

## Fail-before evidence

Command:

```text
bash scripts/verify-automatic-provider-runtime.sh
```

Expected exit status: `1`

Normalized terminal output:

```text
FAIL APR-V01 provider-neutral bootstrap persists no provider credential
missing test: TestInitRejectsProviderFlag
FAIL APR-V02 local development uses loopback and in-memory storage
missing test: TestDevUsesInMemoryBadger
FAIL APR-V03 catalog providers register without operator material
missing test: TestCatalogProviderRegistersWithoutOperatorMaterial
FAIL APR-V04 request policy precedes endpoint binding and authentication
missing test: TestTenantOnlyBindsEndpointFromTenantMaterial
PASS APR-V05 Starmap inference profiles and order drive resolution
FAIL APR-V06 declared cloud chains run outside the inference hot path
missing test: TestProviderReconcilerDiscoversGoogleDefault
FAIL APR-V07 provider reconciliation publishes atomic generations
missing test: TestProviderReconcilerDiscoversAmbientKey
FAIL APR-V08 provider state and failures remain safe and scoped
# ./internal/providerstate
stat internal/providerstate: directory not found
FAIL APR-V09 admin routes report state and trigger reconciliation
missing test: TestAdminProviderStatusContract
FAIL APR-V10 gateway readiness ignores provider credential availability
missing test: TestRuntimeStartsWithoutOperatorCredentials
FAIL APR-V11 the tested quickstart uses current plain commands
FAIL APR-V12 repository and public release readback gates pass
Summary: 1 passed, 11 failed
```

APR-V05 is the preserved green assertion. Existing tests prove that Starmap
environment order drives resolution and inference credentials never enter
catalog state.

## Verified baseline behavior

- Local setup rejects an empty provider and copies resolved provider settings
  into `config.env`. `TestInitializeCreatesNamedIdentity` confirms this current
  behavior.
- Application composition returns `ErrProvidersRequired` for an empty active
  provider set.
- The resolver calls a default cloud chain only after it observes a profile
  through explicit or ambient material. A cloud-only profile is not discovered
  automatically.
- Provider activation and catalog projection bind endpoint templates from
  operator configuration before request credential selection.
- Failure normalization has no provider credential state owner, and the
  `internal/providerstate` package does not exist.
- README quickstart examples still use `STARPORT_BIN` and
  `starport init --provider`.

## Preserved green controls

The following commands passed with normal Go scheduling:

```text
go test ./internal/setup -run '^TestInitializeCreatesNamedIdentity$' -count=1
go test ./internal/config ./internal/app -run '^(TestCatalogCredentialEnvironmentPrecedence|TestInferenceCredentialsNeverEnterCatalogState)$' -count=1
go test ./internal/credentials -run '^TestCredentialResolverWarmCacheHitLatencyAndConcurrency$' -count=1
go test ./internal/registry -run '^TestRuntimeGeneration(RejectsInvalidCandidates|DrainsConnectors)$' -count=1
go test ./internal/providers/byok -run '^TestBYOKStrategyOrderAndUserOnlyNoninterference$' -count=1
go test ./internal/providerauth -run '^(TestGoogleDefaultChainForwardsCancellation|TestCloudChainsAreKeyedOnlyByAuthenticationPrimitive)$' -count=1
bash -n scripts/verify-automatic-provider-runtime.sh
shellcheck scripts/verify-automatic-provider-runtime.sh
```

All seven Go package groups passed. Both shell checks passed. No verification
command used `GOFLAGS=-p` or another scheduler cap.
