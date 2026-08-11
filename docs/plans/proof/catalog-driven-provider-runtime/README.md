# Catalog-driven provider runtime baseline

This proof root belongs to
[`catalog-driven-provider-runtime-plan.html`](../../catalog-driven-provider-runtime-plan.html).
The plan status is `active`. CDP0 starts the evidence phase. Production
implementation has not started.

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
credential sources. Direct secret-store clients must pass the plan budgets
before either binary links them.

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
  is the preferred Vault candidate. OpenBao remains a separate adapter and
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
- [CDP3.1 direct secret sources](cdp3.1.md): four official adapters met all
  admission limits. OpenBao exceeded the source-owner budget. Starmap rejected
  it.
- Per-adapter dependency and binary measurements from CDP7.1.
- CDP3.2 will record the YAML acquisition fail-before scans, contract tests,
  generated-data checks, and uncapped verification results.
