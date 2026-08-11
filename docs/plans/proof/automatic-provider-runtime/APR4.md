# APR4 provider state and failure scope proof

Status: done  
Work commit: `2e894c4`

## Result

APR4 adds one safe provider runtime projection without moving state ownership.

- Adapter activation reports every catalog provider as `ready`,
  `unsupported_transport`, `unsupported_authentication`, or `no_offerings`.
  The compiled transport and authentication registries select behavior. A
  provider ID only labels the result.
- The credential reconciler reports the complete operator credential lifecycle.
  It includes ready, missing, refreshing, denied, invalid, unavailable, and
  retained-prior-material states.
- The availability tracker remains the owner of exact offering circuits. The
  provider state store projects its immutable snapshots.
- Execution publishes the selected credential owner and one opaque material
  version with each attempt outcome. It never publishes credential values,
  source references, or tenant identity.
- Provider authentication failures apply only to the exact current operator
  material version. A later material version clears the failure. A stale
  outcome cannot change the replacement version.
- A provider-proved bad operator material version cannot run again. Request
  policy can select tenant BYOK or another route until reconciliation supplies
  a replacement version.
- Tenant BYOK outcomes remain request-scoped. They cannot change operator
  credential state or shared offering availability.
- Cancellation, unknown failures, and failures without explicit normalized
  scope do not change durable provider state.

The store returns stable, caller-owned JSON data for APR5. It retains internal
material versions only for equality checks. The JSON projection does not
contain them.

The change adds no provider roster, provider-specific branch, module
dependency, compatibility path, or scheduler cap.

## Failure classification evidence

The normalization contract uses provider error fields and conservative state
scope:

- OpenAI documents 401 authentication failures, a distinct
  `credit_balance_exhausted` code, generic rate limits, and unsupported-region
  403 responses. The generic 403 response does not prove a credential-wide
  permission failure. See the
  [OpenAI error code guide](https://developers.openai.com/api/docs/guides/error-codes#api-errors).
- Anthropic documents `authentication_error`, `billing_error`,
  `permission_error`, `rate_limit_error`, `timeout_error`, and transient
  `overloaded_error` types. See the
  [Claude API error guide](https://platform.claude.com/docs/en/api/errors).
- Google documents that Vertex AI 429 responses can mean shared capacity or
  reserved throughput exhaustion. See the
  [Vertex AI 429 guide](https://cloud.google.com/vertex-ai/generative-ai/docs/error-code-429).
- Microsoft documents that Azure OpenAI 429 responses can represent request
  rate, token rate, quota, or transient capacity limits. See the
  [Azure OpenAI quota guide](https://learn.microsoft.com/en-us/azure/foundry/openai/how-to/quota).

The generic connector therefore does not treat an ambiguous 403 as durable
credential evidence. It treats provider request throttling and outages as
exact-offering evidence for operator attempts. Tenant outcomes never enter the
shared circuit.

## Fail-before evidence

The APR0 verifier reported the owning condition as red because the provider
state package did not exist:

```text
FAIL APR-V08 provider state and failures remain safe and scoped
# ./internal/providerstate
stat internal/providerstate: directory not found
```

Before APR4, the reconciler exposed no operator-material state. Authentication
and permission failures did not invalidate an exact material version. One
offering circuit could not distinguish adapter support, credential state, and
provider health.

## Acceptance evidence

The campaign verifier returned the expected APR4 state:

```text
PASS APR-V01 provider-neutral bootstrap persists no provider credential
PASS APR-V02 local development uses loopback and in-memory storage
PASS APR-V03 catalog providers register without operator material
PASS APR-V04 request policy precedes endpoint binding and authentication
PASS APR-V05 Starmap inference profiles and order drive resolution
PASS APR-V06 declared cloud chains run outside the inference hot path
PASS APR-V07 provider reconciliation publishes atomic generations
PASS APR-V08 provider state and failures remain safe and scoped
FAIL APR-V09 admin routes report state and trigger reconciliation
PASS APR-V10 gateway readiness ignores provider credential availability
FAIL APR-V11 the tested quickstart uses current plain commands
FAIL APR-V12 repository and public release readback gates pass
Summary: 9 passed, 3 failed
```

APR-V09, APR-V11, and APR-V12 remain assigned to later tasks. The verifier
exited with status `1` because those planned assertions remain red.

These named APR-V08 tests passed:

```text
TestProviderStateProjectionContract
TestProviderStateRedactsCredentialMaterial
TestFailureTransitionsRespectDocumentedScope
TestMaterialVersionRecovery
```

Additional tests prove complete adapter projection, reconciler state
publication, normalized provider scopes, execution evidence, tenant
noninterference, rejected-version admission, and stable semantic revisions.

## Verification evidence

The following checks passed with normal Go scheduling:

```text
go test ./...                                      42 packages, 36 with tests
go vet ./...                                       pass
make lint                                          0 issues
make build                                         pass
go test -race ./internal/failure \
  ./internal/availability ./internal/execution \
  ./internal/router ./internal/providerstate \
  ./internal/app                                   6 packages
bash scripts/verify-starmap-ownership.sh           12 passed, 0 failed
bash scripts/verify-v1-architecture.sh             12 passed, 0 failed
bash scripts/smoke-openrouter-sdks.sh              4 raw and 3 SDK checks
git diff --check                                   pass
```

No command used `GOFLAGS=-p` or another scheduler cap.

The default autoreview selected Claude Opus 5 high. Its harness stopped before
token use because the local proxy supplied a self-signed certificate. The
secret scan passed. The Sol profile reviewed the exact `2e894c4` substantive
diff with GPT-5.6-sol high. It reported no accepted or actionable findings. It
rated the patch correct with confidence `0.96`.
