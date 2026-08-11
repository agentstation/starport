# CDP7.1 Starport direct secret sources

Status: `done`

Work commit: Starport `b66d415`

## Fail-before evidence

`TestDirectSecretSourceBackendsAreRegistered` failed before the implementation.
Starport did not register these five required backends:

- `gcp-secret-manager`
- `azure-key-vault`
- `aws-secrets-manager`
- `vault`
- `openbao`

Starport also had no reproducible per-adapter build evidence. It had no direct
source refresh setting or catalog-derived reference environment names.

## Accepted source contract

`internal/credentials` now owns all five direct inference secret sources. Each
source uses the official client for its service. Each source also owns client
cleanup and returns typed, secret-free failures.

| Backend | Resource contract | Source authentication |
|---|---|---|
| `gcp-secret-manager` | Global or regional Google secret resource | Google Application Default Credentials |
| `azure-key-vault` | Exact HTTPS vault URL with `/secrets/NAME` | `DefaultAzureCredential` |
| `aws-secrets-manager` | Secret name or ARN | AWS default credential chain |
| `vault` | `MOUNT/PATH` for KV v2 | Vault client environment |
| `openbao` | `MOUNT/PATH` for KV v2 | OpenBao client environment |

The shared reference grammar stays:

```text
backend:resource?version=VERSION#field
```

A reference contains no source authentication value. AWS rejects a resource
that has a URL scheme. Azure rejects user information, query parameters,
fragments, plain HTTP, and unsupported paths. All error conversions omit the
resource, provider message, and secret payload.

Google, Azure, and AWS accept a scalar payload. An exact `#field` selects one
top-level JSON string. The parser rejects duplicate keys, non-string selected
values, trailing JSON, and payloads larger than 1 MiB. Vault and OpenBao accept
one exact KV v2 string. Without `#field`, their record must contain one string.

## Catalog-derived operator configuration

For each Starmap credential field, Starport derives these names:

```text
STARPORT_<PROVIDER>_<FIELD>_REFERENCE
STARPORT_<PROVIDER>_<FIELD>_REFERENCE_FALLBACK_AMBIENT
```

There is no provider roster or `PROVIDER` segment. An explicit programmatic
reference wins over the environment reference. A selected reference precedes
conventional and product ambient values.

The fallback value accepts only `true` or `false`. Only a typed
`not_configured` source result can use a declared ambient fallback. Denial,
invalid data, unavailability, timeout, and cancellation stay terminal.

Alias validation reserves value, reference, and fallback names before the
first environment read. It rejects a collision across providers, fields, or
roles. The effective provider configuration keeps only the reference policy.
It does not contain resolved secret material.

## Lifecycle and performance

The direct sources use the CDP5.1 resolver. They do not add another cache or
single-flight layer. A source result without expiry or lease metadata gets a
renewable five-minute refresh lease. Operators can set
`STARPORT_CREDENTIAL_SOURCES_REMOTE_REFRESH_INTERVAL` to another positive
duration.

Loader tests prove the five-minute default and an explicit nine-minute value.
They also prove that a negative interval fails configuration validation.

The resolver warms each source once. It then serves 10,000 cache hits without
another backend call. The measured local p95 was 167 nanoseconds for each of
the five backends. Sixteen concurrent workers also used the warmed cache
without another backend call. The race detector reported no race.

The refresh boundary caused one new backend call and changed the opaque
material version. Initial and renewed calls use the caller context. Injected
in-flight calls stopped after cancellation with these measured times:

| Backend | Cancellation time |
|---|---:|
| Google | 44.958 microseconds |
| Azure | 71.917 microseconds |
| AWS | 53.834 microseconds |
| Vault | 7.833 microseconds |
| OpenBao | 1.958 microseconds |

Each value is below the 250 millisecond test limit. Every selected client
closes once after its read. Google closes its client. The HTTP clients close
idle connections. No connector stores source clients or credential material.

## Reproducible dependency evidence

The measurement root is
`docs/plans/proof/catalog-driven-provider-runtime/cdp7.1/measure`. The fixed
baseline is Starport commit `613e4fa3a9e912faa064a488d983aaf1f380cee4`.
The harness used Go 1.26.5 and this build shape:

```text
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w'
```

The isolated import measurements produced this result:

| Adapter | Binary bytes | Delta bytes | Delta | Modules | Packages |
|---|---:|---:|---:|---:|---:|
| Baseline | 55,759,010 | 0 | 0.0000% | 102 | 791 |
| Google | 55,759,010 | 0 | 0.0000% | 102 | 791 |
| Azure | 55,759,010 | 0 | 0.0000% | 102 | 791 |
| AWS | 55,759,010 | 0 | 0.0000% | 102 | 791 |
| Vault | 55,759,010 | 0 | 0.0000% | 102 | 791 |
| OpenBao | 55,759,010 | 0 | 0.0000% | 102 | 791 |
| All client imports | 55,759,010 | 0 | 0.0000% | 102 | 791 |

Starmap acquisition already links all five clients into the Starport binary.
Thus, each isolated client import adds no linked module, package, binary byte,
or source repository owner. `owners.tsv` records `(none)` for each delta.

The complete accepted Starport implementation is 55,820,450 bytes. It adds
61,440 bytes, or 0.1102 percent, for adapter and operator code. It still links
102 modules and 791 packages. This is below both binary review thresholds.

The production build has no secret-source build tag. Measurement-only files
use tags after the harness copies them into an archived temporary baseline.
Production tags remain limited to the existing platform file operations.

## Client provenance and review

No new linked source owner exists because all client modules were already in
the fixed baseline. The proof still records each selected client and owner:

| Client module | Version | Source owner | License |
|---|---|---|---|
| `cloud.google.com/go/secretmanager` | v1.21.0 | `googleapis` | Apache-2.0 |
| `github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets` | v1.5.0 | `Azure` | MIT |
| `github.com/aws/aws-sdk-go-v2/service/secretsmanager` | v1.44.5 | `aws` | Apache-2.0 |
| `github.com/hashicorp/vault/api` | v1.23.0 | `hashicorp` | MPL-2.0 |
| `github.com/openbao/openbao/api/v2` | v2.6.0 | `openbao` | MPL-2.0 |

`client-provenance.tsv` records the source repository, commit, tag, and Go
checksum for each module. The live maintenance check ran at
2026-08-11T12:15:00Z. None of the five repositories was archived. Each had a
source push on August 10 or August 11, 2026.

The complete license CSV has 112 rows. The first license command failed because
the proof allowlist omitted Starmap's AGPL-3.0 license. The harness preserved
that result in `licenses-initial.txt`. The corrected evidence allowlist includes
AGPL-3.0 and passed. It is evidence, not an organization policy.

The license scanner reported warnings for assembly files that it cannot inspect
as Go source. The exact client license records are present in `licenses.csv`.
`govulncheck ./...` reported zero reachable vulnerabilities and zero imported
package vulnerabilities. It found one required-module vulnerability that the
compiled code does not call.

All five clients pass the hard architecture and lifecycle gates. No numeric
threshold selected their verdict. Starport accepts OpenBao. No verified
technical, security, license, maintenance, scope, or operational blocker exists.

## Verification

The following commands passed after the final source change:

- `go test ./... -count=1`: 41 packages passed.
- `go vet ./...`.
- `make lint`: zero issues.
- `make build`.
- `bash scripts/verify-starmap-ownership.sh`: 12 passed, 0 failed.
- `bash scripts/verify-v1-architecture.sh`: 12 passed, 0 failed.
- `bash scripts/smoke-first-run.sh`.
- `bash scripts/smoke-openrouter-sdks.sh`: raw HTTP and all three SDKs passed.
- Strict technical-writing checks: six files passed with zero diagnostics.
- `go test ./internal/doclinks ./internal/config -count=1`.
- `shellcheck` for all three measurement scripts.
- `git diff --check`.

This focused race command passed for all six packages:

```text
go test -race ./internal/credentials ./internal/config ./internal/diagnosis ./internal/app ./internal/registry ./internal/router -count=1
```

No verification command used `GOFLAGS`, `-p`, or another scheduler cap.

The campaign verifier reported:

```text
Summary: 18 passed, 1 failed
```

CDP-V01 through CDP-V16 and CDP-V18 through CDP-V19 are green. CDP-V17 is the
only failure. `TestVerifiedRemoteCatalogActivatesProvider` does not exist yet.
CDP8 owns that test and the remote activation contract.
