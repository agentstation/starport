# APR2 catalog-wide provider registration proof

Status: done  
Work commit: `fe3748a`

## Result

APR2 makes catalog eligibility independent of operator credential material.

- Starport registers eligible Starmap providers. Eligibility requires an
  inference service, a non-retired offering, a compiled transport, and a
  compiled authentication primitive.
- Operator configuration is an optional exact-ID join. It does not determine
  provider membership.
- The routable catalog keeps Starmap endpoint templates. It contains no
  operator credential state or endpoint binding.
- Request policy selects operator, tenant, or catalog-default no-auth material
  before Starport binds an endpoint or applies authentication.
- Operator base URL overrides apply only when request policy selects operator
  material.
- An empty operator credential set does not block application startup or
  gateway readiness.
- Unsupported catalog transports and authentication primitives remain
  unavailable without blocking other providers.

The implementation removed the obsolete `ErrProvidersRequired` contract and
the unused configured-provider query. It added no module dependency and no
compatibility path.

## Fail-before evidence

After APR1, the campaign verifier reported `3 passed, 9 failed`. APR-V03,
APR-V04, and APR-V10 failed because these acceptance tests did not exist:

```text
TestCatalogProviderRegistersWithoutOperatorMaterial
TestTenantOnlyBindsEndpointFromTenantMaterial
TestRuntimeStartsWithoutOperatorCredentials
```

The prior catalog projection also bound operator base URLs and endpoint values
before request credential selection. The registry rejected a provider without
an operator material source and could not publish an empty provider set.

## Acceptance evidence

The campaign verifier returned the expected APR2 state:

```text
PASS APR-V01 provider-neutral bootstrap persists no provider credential
PASS APR-V02 local development uses loopback and in-memory storage
PASS APR-V03 catalog providers register without operator material
PASS APR-V04 request policy precedes endpoint binding and authentication
PASS APR-V05 Starmap inference profiles and order drive resolution
PASS APR-V10 gateway readiness ignores provider credential availability
Summary: 6 passed, 6 failed
```

APR-V06 through APR-V09, APR-V11, and APR-V12 remain assigned to later tasks.
The verifier exited with status `1` because those planned assertions are still
red.

These named tests passed:

```text
TestCatalogProviderRegistersWithoutOperatorMaterial
TestNoAuthProviderRegistersWithoutMaterial
TestTenantOnlyBindsEndpointFromTenantMaterial
TestOperatorAndTenantBindingsDoNotCross
TestUserOnlySkipsOperatorResolution
TestRuntimeStartsWithoutOperatorCredentials
TestReadinessIgnoresProviderCredentialAvailability
```

The endpoint tests use distinct operator and tenant base URLs, project fields,
and material versions. They prove that tenant-only policy does not read or use
operator state. They also prove that one selected material value supplies both
endpoint binding and authentication for each request.

## Verification evidence

The following checks passed with normal Go scheduling:

```text
go test ./...                                      41 packages, 35 with tests
go vet ./...                                       pass
make lint                                          0 issues
make build                                         pass
go test -race ./internal/catalog ./internal/providers \
  ./internal/registry ./internal/router \
  ./internal/app ./internal/server                 6 packages
bash scripts/verify-starmap-ownership.sh           12 passed, 0 failed
bash scripts/verify-v1-architecture.sh             12 passed, 0 failed
bash scripts/verify-developer-experience.sh        41 passed, 0 failed
bash scripts/verify-doc-links.sh                    pass
bash scripts/smoke-first-run.sh                    pass
bash scripts/smoke-openrouter-sdks.sh              4 raw and 3 SDK checks
bash -n on changed verifier scripts                pass
shellcheck on changed verifier scripts             pass
technical-writing lint on changed durable text     6 files, 0 diagnostics
git diff --check                                   pass
```

The SDK smoke covered the official Python, TypeScript, and Go clients.

The default autoreview selected Claude Opus 5 high. Its harness stopped before
token use because the local proxy supplied a self-signed certificate. The
secret scan passed. The Sol profile then reviewed the same branch diff with
GPT-5.6-sol high and reported no accepted or actionable findings. Its overall
correctness confidence was `0.93`.

No check used `GOFLAGS=-p` or another scheduler cap.
