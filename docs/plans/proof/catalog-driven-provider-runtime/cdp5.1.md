# CDP5.1 Starport credential material and core sources

Status: `done`

Work commit: Starport `518c783`

## Fail-before evidence

- `providerauth.Token` stored one bearer value and a Google quota-project
  field.
- `providerauth.NewRefreshingSource` owned a second refresh cache.
- Static material required a token expiry before it could enter that cache.
- Starport configuration stored one raw API key, authentication mode, profile
  ID, and primitive for each provider.
- Google and Azure connectors could create their own default-identity sources.
- Explicit environment and file references, typed fallback, file rotation,
  lifecycle revocation, and warmed-cache evidence did not exist in Starport.

## Named material and one lifecycle

`internal/credentials` now owns named inference material. A material value has
one selected Starmap profile, private field values, an opaque version, an
optional expiry, and an optional renewable lease. Static material has no
expiry.

One resolver owns source selection, cache hits, single-flight initial work,
expiry, lease refresh, explicit refresh, and revocation. A revocation epoch
prevents in-flight source work from repopulating an invalidated cache. A
failed refresh retains the last valid material. A waiter retries when the
single-flight leader cancels its context.

The configuration loader creates this resolver after it reads the active
environment and configuration-file layers. Application and diagnosis
composition then resolve material from the exact active Starmap catalog. The
configuration tree no longer stores a raw provider API key, profile ID,
primitive, or authentication mode.

`providerauth` is now a thin bearer projection from one catalog-declared
material field. It has no cache. Authentication primitives key the Google and
Azure default chains. They use catalog scopes and return named renewable
material. The temporary connector projection remains until CDP6 removes
connector-held credentials. A connector cannot create a separate default
cloud source.

## References and core sources

The tagged reference grammar is:

```text
backend:resource?version=VERSION#field
```

Starport includes environment and file sources. An explicit reference precedes
ambient discovery. A missing explicit source fails closed unless the field
policy permits fallback for the typed `not_configured` result. Denial,
invalid material, unavailability, and cancellation do not fall back.

File reads require an absolute path, a regular file, and at most 1 MiB. They
preserve exact bytes. Source errors contain the backend and typed failure, but
not the resource path or credential value.

## Contract tests

`TestCredentialSourceConformance` runs the same 14 vector IDs as Starmap:

```text
static,default_chain,version,expiry,lease,cancellation,concurrency,denial,redaction,rotation_in_place,rotation_atomic_replace,rotation_symlink_swap,rotation_mounted_replace,rotation_agent_rerender
```

The five rotation vectors separately prove in-place rewrite, atomic file
replacement, direct symlink target swap, projected-volume `..data` swap, and
agent rerender by rename. Each vector changes the source while preserving the
file modification time, calls the explicit refresh operation, and proves that
the opaque material version changes.

`TestCredentialResolverWarmCacheHitLatencyAndConcurrency` warms the resolver
once and resolves credentials from the cache 10,000 times. It makes no more
backend calls. The local p95 stayed below 1 millisecond. The test then makes 6,400
concurrent cache hits. The race detector reported no race.

Other named tests prove these contracts:

- `TestExplicitCredentialReferencesPrecedeAmbientSources` proves precedence,
  fail-closed behavior, typed fallback, and exact file bytes.
- `TestCredentialLifecycleRefreshFailureRevocationAndLeaderCancellation`
  proves failure retention, revocation during in-flight work, and waiter
  recovery after leader cancellation.
- `TestCredentialResolverRefreshesExpiredAndLeasedMaterial` proves expiry and
  lease renewal as separate source calls.
- `TestFileSourceEnforcesPathAndSizePolicy` proves the absolute-path,
  regular-file, and size limits.
- `TestInferenceCredentialsNeverEnterCatalogState` proves that neither
  inference nor acquisition values enter serialized catalog state.

## Verification

These checks passed:

- `go test ./...` across all Starport packages.
- `go vet ./...`.
- `make lint` with zero issues.
- `make build`.
- `bash scripts/verify-starmap-ownership.sh`: 12 passed, 0 failed.
- `bash scripts/verify-v1-architecture.sh`: 12 passed, 0 failed.
- `bash scripts/smoke-first-run.sh`.
- `bash scripts/smoke-openrouter-sdks.sh`, including raw HTTP and the Python,
  TypeScript, and Go OpenRouter SDKs.
- `git diff --check`.

This focused race command passed:

```text
go test -race ./internal/credentials ./internal/providerauth ./internal/config ./internal/providers ./internal/providers/connectors ./internal/app ./internal/diagnosis
```

The uncapped run completed `internal/credentials` in 5.488 seconds and
`internal/providerauth` in 1.577 seconds. It completed `internal/config` in
22.478 seconds and `internal/providers` in 1.762 seconds. It completed
`internal/providers/connectors` in 13.887 seconds, `internal/app` in 27.304
seconds, and `internal/diagnosis` in 18.490 seconds. No race report occurred.
No command used `GOFLAGS`, `-p`, a scheduler cap, or a timeout change.

The campaign verifier reported:

```text
Summary: 9 passed, 10 failed
```

CDP-V02, CDP-V06, and CDP-V07 became green. CDP6 owns the next red conditions
for primitive registries, credential-free connectors, request isolation, and
the synthetic provider inference contract.

## Starmap pull-request audit

The live GitHub API reported zero open Starmap pull requests. The repository
merged pull requests 68, 70, 71, and 72. Dependabot closed pull request 69 as
superseded. Starmap `main` has newer versions of AWS SDK for Go v2, Amazon S3,
Smithy, and Google Gen AI.
