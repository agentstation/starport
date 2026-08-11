# Catalog-driven provider runtime baseline

This proof root belongs to
[`catalog-driven-provider-runtime-plan.html`](../../catalog-driven-provider-runtime-plan.html).
The plan status is `active`. CDP7.1 is the current task.

## Pinned source state

| Repository | Branch | Commit | Observation |
|---|---|---|---|
| Starport | `main` | `101c32d8fd6991586e8ae7003baf199cef651844` | The worktree was clean on 2026-08-10. |
| Starmap | `origin/main` | `7f53767dfb68efbde2cec80c3d739f5badb43230` | The reviewed local branch had the same tree. |

## Baseline observations

- Starmap provider YAML declares 14 providers.
- Starport configuration and adapter descriptors declare eight providers.
- Six Starmap providers use the OpenAI transport but lack Starport descriptors.
- Starmap has no serializable inference credential contract.
- Starport duplicates provider credential fields, placement, and validation.
- Starmap Google acquisition contains compiled model IDs, limits, and family rules.
- Starmap CLI credential discovery uses fixed environment variable lists.
- Starport setup and environment configuration use a fixed provider structure.
- `bash scripts/verify-starmap-ownership.sh` reported `12 passed, 0 failed`.
- The verifier does not test an unknown provider from YAML through inference.

## Secret-source research

Core V1 uses ambient, explicit `env:`, explicit `file:`, and cloud default
credential sources. Direct secret-store clients must pass the plan hard gates
before either binary links them. Numeric review thresholds require review but
do not select the result.

- Reject [Helmfile vals v0.45.0](https://github.com/helmfile/vals/tree/v0.45.0).
  It requires Go 1.26 and has a broad provider closure. Its provider API does
  not supply the required common context and lifecycle contract.
- [Go CDK runtimevar v0.46.0](https://github.com/google/go-cloud/tree/v0.46.0/runtimevar)
  is a comparator, not the selected abstraction. It includes AWS Secrets
  Manager, AWS Parameter Store, Google Cloud Secret Manager, filevar, and
  HashiCorp Vault. It does not include an equivalent Azure runtimevar package.
- Official AWS, Google Cloud, and Azure clients remain the direct-adapter
  baseline.
- [`github.com/hashicorp/vault/api`](https://pkg.go.dev/github.com/hashicorp/vault/api)
  is the selected Vault client.
- [`github.com/openbao/openbao/api/v2`](https://openbao.org/api-docs/libraries/)
  is the official maintained OpenBao client and remains a separate adapter and
  conformance target.

The repository-owned credential layer owns caching, resolution, versions,
expiry, leases, cancellation, fallback, and redaction. It also owns
single-flight work. `providerauth.Source` becomes a bearer-token projection and
does not own a second cache.

## Dependency baseline

The controlled build used Go 1.26.5 with this command shape:

```text
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w'
```

| Repository | Binary bytes | Modules | Compiled packages |
|---|---:|---:|---:|
| Starport | 49,791,138 | 157 | 609 |
| Starmap | 36,769,954 | 126 | 570 |

The review disposition records the measurement qualifications, Claude's
unverified adapter deltas, and the revised admission limits. Raw module and
package deltas are mandatory evidence, but they are not admission limits.

## Evidence

- [CDP0 red verifier](cdp0.md): `Summary: 0 passed, 19 failed`.
- [CDP3 credential sources](cdp3.md): the full uncapped Starmap gate passed,
  and the campaign verifier reported `Summary: 3 passed, 16 failed`.
- [CDP3.1 direct secret sources](cdp3.1.md): five direct adapters passed the
  hard gates. OpenBao's six owners triggered and passed the required dependency
  and security review.
- [CDP3.2 YAML acquisition contract](cdp3.2.md): YAML drives a synthetic
  provider with no provider branch. Acquisition, mapping, trust review,
  publication, and the final uncapped gate passed.
- [CDP4 atomic remote subscriber](cdp4.md): one atomic state, caller-owned
  durability, pinned bootstrap, degraded recovery, and the full uncapped gate
  passed.
- [CDP4.1 Starmap release and catalog publication](cdp4.1.md): CDP4.1 merged
  all pending Starmap pull requests. Hosted checks passed before each merge.
  Starmap v0.4.0, Homebrew, and the exact Starport module passed public
  readback. An immutable schema-v5 catalog passed provenance and compatible
  rollback verification.
- [CDP5 Starport dynamic provider configuration](cdp5.md): the active Starmap
  snapshot now drives provider environment resolution. Conventional and
  product aliases, collision rejection, Fireworks discovery, configuration
  inspection, and the uncapped focused race gate passed.
- [CDP5.1 Starport credential sources](cdp5.1.md): the source and cache
  lifecycle contract passed. Shared conformance, file rotation, and the
  warmed-cache contract also passed.
- [CDP6 credential-free runtime primitives](cdp6.md): catalog facts activate
  compiled transport and authentication primitives without a provider roster.
- [CDP6.1 atomic runtime generations](cdp6.1.md): complete candidates, request
  leases, and cache leases passed. Rollback, connector draining, and credential
  rotation also passed.
- [CDP7 request credential policy and operator surfaces](cdp7.md): exact BYOK
  order, tenant isolation, and neutral availability handling passed.
  Catalog-driven surfaces passed for chat, streaming, and embeddings.
- Per-adapter dependency, binary, lifecycle, and conformance evidence from
  CDP7.1.
