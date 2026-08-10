# DX9 completion-gate proof

Date: 2026-08-10

## Scope

DX9 tested the complete developer path from the merged `main` branch at
`d7bfae4`. The evidence covers local initialization, diagnosis, server
readiness, authenticated model discovery, OpenRouter client contracts,
release artifacts, repository gates, current documents, and shared storage.

## First-run and client scenes

These commands passed:

```bash
bash scripts/smoke-first-run.sh
bash scripts/smoke-openrouter-sdks.sh
```

The first-run scene used an isolated configuration root and an allowlisted
environment. It initialized one named identity, validated the configuration,
probed the storage, started Starport, checked readiness, and read the
authenticated OpenRouter model catalog.

The client scene passed raw HTTP chat, stream, models, and embeddings
requests. The pinned official Python, TypeScript, and Go OpenRouter clients
also passed.

## Repository gates

These required commands passed:

```bash
bash scripts/verify-starmap-ownership.sh
bash scripts/verify-v1-architecture.sh
go test ./...
go vet ./...
make lint
make build
bash scripts/smoke-openrouter-sdks.sh
```

The ownership verifier passed 12 checks. The architecture verifier passed 12
checks. The developer-experience verifier passed all 41 fixed checks. The
release verifier passed 14 checks. The release workflow, action-pin,
document-link, and link-parser fixture checks passed.

`make check` and `make check-race` passed. The race gate reported no data
race. The dedicated Valkey integration target tested storage, cache,
application, identity, credential, preset, and rate-limit repository
contracts. Its trap removed the test container, network, and volume.

## Release and install evidence

GoReleaser 2.17.1 built a clean `v1.0.1-next` snapshot. The snapshot passed
these checks:

- Six exact-version, CGO-disabled binaries covered the macOS, Linux, and
  Windows AMD64 and ARM64 matrix.
- Six archives, six Syft SBOMs, and the checksum manifest passed.
- The generated Homebrew cask passed syntax, platform, checksum, installed
  artifact, and strict audit checks.
- The release workflow and 16 action pins passed their contract checks.

The local Homebrew installation has GoReleaser 2.15.3. The release gate first
rejected that version. The final run installed GoReleaser 2.17.1 into an
isolated temporary tool directory and removed the directory after the
snapshot. This result proves that the version gate fails closed.

GitHub reports public release `v1.0.0` with 13 assets: one checksum manifest,
six platform archives, and six SBOMs. GitHub also reports `main` as the
default branch for `agentstation/starport` and `agentstation/homebrew-tap`.

## Documentation review

Strict technical-writing lint passed these current documents and the active
plan with zero diagnostics:

```text
README.md
DEVELOPMENT.md
MODELS.md
SECURITY.md
docs/ARCHITECTURE.md
docs/CACHE_CONTROL.md
docs/CONTRIBUTING.md
docs/OPERATOR-GUIDE.md
docs/README.md
docs/TASKS.md
docs/VERTEX_AI_CONFIG.md
internal/config/README.md
docs/plans/starport-developer-experience-plan.html
```

The glossary check passed 15 terms with zero errors. The link verifier found
no broken current-document link. Its syntax fixtures passed.

The factual review compared the README and development commands with the CLI
help, Make targets, smoke scripts, release configuration, and public GitHub
metadata. The documented `init`, `serve`, `doctor`, `config`, `completion`,
`man`, and `version` commands match the CLI. The documented `/v1` and
`/api/v1` base URLs match the server contracts. Active branch procedures use
`main`. Occurrences of "master" in the current guides refer only to the
provider-credential master key.

## Unverified external scenes

- `UNVERIFIED`: Valkey pub/sub recovery after an external service restart
  needs a controlled restart during the test. All non-restart Valkey
  contracts passed.
- `UNVERIFIED`: a paid live-provider inference request needs an operator-owned
  provider key. Mock transport, protocol, routing, and SDK contracts passed.

No unavailable external scene changed a required assertion or hid a failure.
