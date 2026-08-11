# APR5 provider operations API proof

Status: done  
Work commit: `62ed730`

## Result

APR5 exposes the APR3 and APR4 application ports through two authenticated
administrative routes:

- `GET /api/v1/admin/providers` returns one safe provider state snapshot.
- `POST /api/v1/admin/providers/refresh` forces one shared provider credential
  reconciliation with the request context.

Both routes require a valid active gateway API key with the `admin` scope. The
existing OpenRouter protocol middleware supplies authentication and permission
errors. An unauthenticated or non-admin request does not call the provider
operations port.

The status response contains the provider state revision, catalog generation,
adapter state, operator credential state, and exact offering state. The APR4
projection excludes credential values, material versions, source references,
and tenant identity.

The refresh response contains the reconciliation revision, change flag,
configured provider IDs, failure count, and the provider state revision before
and after the operation. It does not serialize internal reconciliation errors.
Canceled and expired request contexts stop the shared work and return safe
OpenRouter errors. Both routes set `Cache-Control: no-store`.

The HTTP server requires the provider operations port as a ready dependency.
Production composition supplies the application directly. The controller owns
only transport conversion and does not duplicate reconciliation or provider
state policy.

The change adds no provider roster, provider-specific branch, module
dependency, compatibility path, or scheduler cap.

## Fail-before evidence

The APR4 campaign verifier reported the owning condition as red:

```text
FAIL APR-V09 admin routes report state and trigger reconciliation
missing test: TestAdminProviderStatusContract
```

Before APR5, both exact routes returned `404`. The application ports existed,
but the HTTP server did not receive or expose them.

## Acceptance evidence

The campaign verifier returned the expected APR5 state:

```text
PASS APR-V01 provider-neutral bootstrap persists no provider credential
PASS APR-V02 local development uses loopback and in-memory storage
PASS APR-V03 catalog providers register without operator material
PASS APR-V04 request policy precedes endpoint binding and authentication
PASS APR-V05 Starmap inference profiles and order drive resolution
PASS APR-V06 declared cloud chains run outside the inference hot path
PASS APR-V07 provider reconciliation publishes atomic generations
PASS APR-V08 provider state and failures remain safe and scoped
PASS APR-V09 admin routes report state and trigger reconciliation
PASS APR-V10 gateway readiness ignores provider credential availability
FAIL APR-V11 the tested quickstart uses current plain commands
FAIL APR-V12 repository and public release readback gates pass
Summary: 10 passed, 2 failed
```

APR-V11 and APR-V12 remain assigned to APR6 and APR7. The verifier exited with
status `1` because those planned assertions remain red.

These named APR-V09 tests passed:

```text
TestAdminProviderStatusContract
TestAdminProviderRefreshContract
TestAdminProviderRoutesRequireAuthentication
```

The tests also prove no-store responses, safe OpenRouter errors, internal-error
redaction, and prompt cancellation without a retained goroutine.

## Verification evidence

The following checks passed with normal Go scheduling:

```text
go test ./...                                      42 packages, 36 with tests
go vet ./...                                       pass
make lint                                          0 issues
make build                                         pass
go test -race ./internal/server \
  ./internal/server/controllers ./internal/app     3 packages
bash scripts/verify-starmap-ownership.sh           12 passed, 0 failed
bash scripts/verify-v1-architecture.sh             12 passed, 0 failed
bash scripts/smoke-openrouter-sdks.sh              4 raw and 3 SDK checks
git diff --check                                   pass
```

No command used `GOFLAGS=-p` or another scheduler cap.

The default autoreview selected Claude Opus 5 high. Its harness stopped before
token use because the local proxy supplied a self-signed certificate. The
secret scan passed. The Sol profile reviewed the exact `62ed730` substantive
diff with GPT-5.6-sol high. It reported no accepted or actionable findings. It
rated the patch correct with confidence `0.98`.
