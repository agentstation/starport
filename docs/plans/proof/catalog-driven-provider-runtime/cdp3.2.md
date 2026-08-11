# CDP3.2 Starmap YAML acquisition and mapping contract

Status: `done`

Work commit: Starmap `ef79cc382d93787a3794bd385f75ef9c6e030ce8`

## Fail-before evidence

- No synthetic test proved the complete YAML-only provider acquisition path.
- The OpenAI-compatible mapper accepted two noncanonical destination aliases.
- Unconditional Go defaults duplicated provider YAML limit mappings.
- Some invalid mapping contracts could reach credential resolution or network
  work before failure.
- `ProviderCatalog.Authors` existed in YAML and public schema but had no
  production acquisition consumer.
- Ollama declared catalog acquisition without a compiled acquisition adapter.
- Unknown discovered offerings failed inside the provider source instead of
  reaching the reconciler review-candidate seam.
- OpenBao was absent only because an arbitrary source-owner count served as
  an automatic rejection rule.

## YAML acquisition contract

A provider needs no Go change when it uses a compiled transport and
authentication primitive. Its facts must fit that transport's typed catalog
schema. A new transport, authentication primitive, or unsupported wire field
can require Go.

`TestYAMLOnlyProviderAcquisitionPublishesReviewedOfferingAndQuarantinesUnknown`
uses a synthetic `acme` provider and the existing OpenAI-compatible transport.
It proves these facts:

- YAML selects the exact `/models` endpoint.
- YAML declares `ACME_API_KEY` and its bearer placement.
- The client retrieves provider models with no provider ID constant or branch.
- `field_mappings` set the name, context window, and output-token limit.
- `author_mapping` maps `owned_by` to an existing canonical author.
- Provider model IDs remain exact and opaque.
- An exact offering with `model: acme-labs/acme-model` publishes.
- An unknown exact ID stays unpublished and becomes a durable review
  candidate with observation and checksum evidence.

The pipeline now copies human-authored authors and model definitions into the
provider configuration catalog. This change lets a YAML workspace validate
author mapping targets before acquisition.

## Mapping authority

Transport adapters own typed wire decoding, the supported source vocabulary,
canonical target writers, and contract validation. Provider YAML owns
provider-specific activation and interpretation.

The OpenAI-compatible adapter keeps the wire fields `context_window` and
`max_completion_tokens`. Groq uses these current provider catalog fields. YAML
maps them to `limits.context_window` and `limits.output_tokens`.

The mapper no longer accepts the destination aliases `context_window` and
`max_completion_tokens`. Repository, history, fixture, generated-data, and
release-artifact scans found no contract that used them. The unconditional
provider-limit fallback is also gone. The first configured, present source in
YAML order owns each destination.

The validation seam now rejects these forms before credential or network work:

- Unknown or type-incompatible field mapping sources and destinations.
- Unsupported author selectors for the selected transport.
- Empty maps, invalid globs, and case-fold duplicate author keys.
- Author targets that do not identify a canonical catalog author.
- Unsupported capability mapping sources, targets, evidence, or combination
  rules.
- Catalog acquisition for a transport that has no compiled adapter.

This change deletes `ProviderCatalog.Authors` as a direct prelaunch schema
break. Ollama keeps its inference endpoint. The embedded YAML no longer
declares an Ollama catalog endpoint. Contract validation rejects a new Ollama
catalog declaration before I/O.

## Capability evidence

The removed schema-v5 `feature_rules` remain absent. Model IDs, author names,
family names, tags, and free text cannot create capability facts.

Typed `capability_mappings` accept exact provider predicates, canonical target
sets, an HTTPS provider-contract reference, and a deterministic combination
rule. The rules are `conflict`, `first-known`, `any`, and `all`. The mapping
preserves true, false, and unknown states. Contradictory known claims fail.

Fireworks YAML maps `supports_tools` to `tools`, `tool_calls`, and
`tool_choice`. Its cited provider contract supports that semantic entailment.
The generic OpenAI-compatible adapter does not apply these semantics to another
provider. Groq `supported_features` behavior remains unchanged because its
bundle semantics lack a verified provider contract.

## Direct secret sources

The policy correction admits OpenBao after a mandatory dependency and security
review. The final implementation uses
`github.com/openbao/openbao/api/v2@v2.6.0`. Vault and OpenBao share one typed KV
v2 lifecycle seam. Each adapter keeps its official client and vendor error
mapping.

The OpenBao dependency closure adds these six source repository owners:

- `github.com/cenkalti`
- `github.com/go-jose`
- `github.com/hashicorp`
- `github.com/mitchellh`
- `github.com/openbao`
- `github.com/ryanuber`

The owner count triggered a review. The review found no technical, security,
license, maintenance, architecture, or operational blocker.

The final stripped Linux binary is 43,905,186 bytes. Its aggregate increase
from the pinned baseline is 3,690,496 bytes, or 9.1770%. The binary links 107
modules and 803 compiled packages. Each adapter remains below the 8%
per-adapter review threshold. The aggregate remains below the 15% review
threshold.

The warm-cache test resolves once and then reads the cache 10,000 times. It
asserts one total backend call and a local p95 of 3.042 microseconds. The test
also runs concurrent cache hits under the race detector.

## Generated data

The generated embedded catalog has these values:

- Generation ID: `catalog-20260811T031305Z-e9ff340d7b9f`.
- Payload SHA-256:
  `4681b6c753a04e6c50d978448e48eb326a8d6284e637f6a0e23fdd6aca312dc0`.
- Semantic SHA-256:
  `e9ff340d7b9f224e4ce851189ac781f0145f9ab5a4ec176863708d8cf19346e9`.
- Payload size: 8,110,865 bytes.
- Pinned archive SHA-256:
  `8c52cacc9bc675e076e7b04cdc3c25ff788221d37b36d653c5b72957bb53a648`.

Generated Go documentation and OpenAPI describe `capability_mappings` and no
longer describe `ProviderCatalog.Authors`.

## Security evidence

This command passed:

```text
go run github.com/google/go-licenses@v1.6.0 check ./cmd/starmap \
  --allowed_licenses=Apache-2.0,BSD-2-Clause,BSD-3-Clause,ISC,MIT,MPL-2.0 \
  --ignore=github.com/agentstation/starmap
```

The command reported only assembly-file inspection warnings. The license list
is scan evidence, not a plan-local organization policy.

`go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...` found no reachable
vulnerability and no vulnerability in an imported package. It found one
required-module vulnerability that Starmap does not call.

## Verification

The final uncapped `make verify` passed:

- All 85 ordinary packages.
- Six external pure-Go consumer modules and the S3 package.
- The complete repository race suite with `CGO_ENABLED=1`.
- `go vet ./...`.
- `golangci-lint` with zero issues.
- Three catalog benchmarks at 8.579, 8.316, and 8.739 ns/op.
- Zero bytes and zero allocations for each catalog benchmark.
- All 15 coverage gates.
- Generated documentation, OpenAPI, and embedded catalog checks.
- File-size, whitespace, build, catalog validation, and CLI smoke checks.

The race run completed the root package in 280.251 seconds,
`internal/catalog/pipeline` in 14.183 seconds, `pkg/catalogs` in 68.459
seconds, and `internal/auth` in 2.436 seconds. No race report occurred. No
command used `GOFLAGS`, `-p`, a scheduler cap, or a timeout change.

Strict writing checks passed for each changed README and architecture section.
The complete plan and proof set also passed the strict writing check. The
repository documentation check passed.

## Campaign verifier

`bash scripts/verify-catalog-driven-providers.sh` reported:

```text
Summary: 3 passed, 16 failed
```

CDP-V14 now runs the Starmap synthetic YAML acquisition test before the
Starport synthetic inference test. The Starmap test passes. The condition
remains red because CDP6 owns the missing Starport test. CDP-V01, CDP-V03, and
CDP-V10 remain green.
