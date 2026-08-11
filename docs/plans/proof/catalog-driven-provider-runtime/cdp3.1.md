# CDP3.1 Starmap direct secret-source adapters

Status: `done`

Work commit: Starmap `4b556428e800b2b16a8dcd06a8c90bcfe6a05402`

## Fail-before evidence

- Starmap had no direct secret-store adapter with reproducible dependency,
  binary, lifecycle, license, or vulnerability evidence.
- The initial comparison used one shared candidate graph. It could not prove
  the cost of each adapter in isolation.
- No source conformance test proved client closure, cancellation, typed
  failures, payload handling, or error redaction for direct secret stores.

## Admission method

The reproducible harness is in
[`measure/`](cdp3.1/measure/). It archives the Starmap baseline at
`54bd8de9ea9f6c26188bf2ebb54dc5f647758ef9` into a separate module graph for
each candidate. Each graph uses Go 1.26.5 and this build shape:

```text
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w'
```

The harness records complete module lists, compiled-package lists, build
information, normalized source owners, and binary sizes. The raw evidence is
in [`measure/raw-starmap/`](cdp3.1/measure/raw-starmap/).

## Candidate results

| Adapter | Official module | New source owners | Binary delta | Result |
|---|---|---:|---:|---|
| Google Secret Manager | `cloud.google.com/go/secretmanager@v1.21.0` | 0 | 4.5325% | `accepted` |
| Azure Key Vault | `github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets@v1.5.0` | 0 | 0.2343% | `accepted` |
| AWS Secrets Manager | `github.com/aws/aws-sdk-go-v2/service/secretsmanager@v1.44.5` | 0 | 0.5500% | `accepted` |
| HashiCorp Vault | `github.com/hashicorp/vault/api@v1.23.0` | 5 | 1.1000% | `accepted` |
| OpenBao | `github.com/openbao/openbao/api/v2@v2.6.0` | 6 | 1.0796% | `rejected(budget)` |

The Vault adapter uses the complete five-owner allowance:

- `github.com/cenkalti`
- `github.com/go-jose`
- `github.com/hashicorp`
- `github.com/mitchellh`
- `github.com/ryanuber`

OpenBao adds `github.com/openbao` to that set. It exceeds the five-owner limit.
A later direct-secret-source plan owns OpenBao support. Starmap does not include
an OpenBao runtime dependency or compatibility path.

The isolated candidate binaries were:

| Graph | Binary bytes | Delta bytes | Delta | Modules | Packages |
|---|---:|---:|---:|---:|---:|
| Baseline | 40,214,690 | 0 | 0.0000% | 79 | 711 |
| Google | 42,037,410 | 1,822,720 | 4.5325% | 88 | 768 |
| Azure | 40,308,898 | 94,208 | 0.2343% | 81 | 714 |
| AWS | 40,435,874 | 221,184 | 0.5500% | 80 | 714 |
| Vault | 40,657,058 | 442,368 | 1.1000% | 94 | 739 |
| OpenBao | 40,648,866 | 434,176 | 1.0796% | 93 | 738 |
| Accepted candidates | 42,778,786 | 2,564,096 | 6.3760% | 105 | 801 |
| All candidates | 42,885,282 | 2,670,592 | 6.6408% | 107 | 803 |

The final implemented Starmap binary is 42,823,842 bytes. Its 2,609,152-byte
delta is 6.4881%. It contains 105 modules and 801 compiled packages. Each
accepted adapter is below the 8% individual limit. The implemented aggregate
is below the 15% limit.

All candidates preserve the Go 1.25 language floor. The accepted runtime
closure contains no new Kubernetes, SOPS, CLI, template, or unrelated
secret-backend family. Six external consumer modules proved that direct adapter
packages do not enter read-only, store-only, pinned-artifact, server, remote,
or server-storage consumers.

## Delivered contract

Starmap now compiles these direct read sources:

- `gcp-secret-manager`
- `azure-key-vault`
- `aws-secrets-manager`
- `vault`

Each source creates its official client only for a selected resolution. It
closes its owned client or idle HTTP resources after the read. The source
adapters start no goroutines. The tests assert that every selected fake client
closes exactly once. Unselected adapters only retain constructor closures.

References contain resource identity, an optional version, and an optional
field. They never contain source authentication values. Google, Azure, and AWS
preserve the complete scalar payload when the reference selects no field. An exact field
selects one top-level JSON string. Duplicate keys, non-string selected values,
trailing JSON, and payloads larger than 1 MiB fail as invalid. Vault reads KV
v2 data and requires one exact string when the reference selects no field.

Failures map to the existing typed source kinds: `not_configured`, `denied`,
`invalid`, and `unavailable`. Cancellation and deadlines remain context
errors. Error strings do not include references, payloads, provider response
messages, or source authentication values.

## Lifecycle and latency evidence

The deterministic local fake returned from cancellation in:

| Adapter | Cancellation time |
|---|---:|
| Google | 71.167 microseconds |
| Azure | 2.917 microseconds |
| AWS | 1.417 microseconds |
| Vault | 0.958 microseconds |

Each result is below the 250-millisecond limit. Across 10,000 cold fake calls,
resolver overhead had a p95 of 834 nanoseconds. This result is below the
1-millisecond limit.

## License and vulnerability evidence

This license command passed:

```text
go run github.com/google/go-licenses@v1.6.0 check ./cmd/starmap \
  --allowed_licenses=Apache-2.0,BSD-2-Clause,BSD-3-Clause,ISC,MIT,MPL-2.0 \
  --ignore=github.com/agentstation/starmap
```

The tool reported only inspection warnings for assembly files. It reported no
disallowed license.

`go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...` reported zero reachable
vulnerabilities and zero vulnerabilities in imported packages. It found one
vulnerability in the required module graph, but no Starmap code calls the
affected symbol.

## Verification history

The first uncapped `make verify` run passed the ordinary test suite. The
pure-Go consumer gate then found stale checksums in six nested consumer
modules. `go mod tidy` updated their `go.sum` files without changing their
`go.mod` contracts. The pure-Go gate then passed and proved that consumers did
not link the new adapter families.

A later full race run produced no race report, but the root package reached the
20-minute wall-clock timeout while the machine was inactive. The exact root
test passed uncapped and unchanged in 54.546 seconds. The complete uncapped
race rerun then passed. The root package completed in 273.139 seconds. This
evidence identifies an environmental wall-clock interruption, not a data race
or a need for a scheduler cap. We used no `GOFLAGS`, `-p`, or longer timeout.

The final `make verify` passed:

- All 85 ordinary packages.
- Six isolated pure-Go consumer modules and the S3 package.
- The complete uncapped repository race suite with `CGO_ENABLED=1`.
- `go vet ./...`.
- `golangci-lint` with zero issues.
- Three catalog-access benchmarks at 8.768, 9.240, and 9.243 ns/op.
- Zero bytes and zero allocations for each catalog-access benchmark.
- All 15 critical seam coverage gates.
- Generated documentation, catalog data, OpenAPI, file-size, whitespace,
  build, catalog validation, provider-list, and model-list checks.

The strict writing check for the new README section passed with zero
diagnostics. The repository documentation gate also passed.

## Campaign verifier

`bash scripts/verify-catalog-driven-providers.sh` reported:

```text
Summary: 3 passed, 16 failed
```

CDP-V01, CDP-V03, and CDP-V10 pass. Later Starmap and Starport runtime tasks
own the remaining conditions.
