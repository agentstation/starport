# Contribute to Starport

Starport accepts focused fixes, tests, documentation, and provider-runtime
work. Open an issue before a large behavior or architecture change.

Obey the [community rules](CODE_OF_CONDUCT.md). Report security defects
through the private process in [SECURITY.md](../SECURITY.md).

## Set up a fork

Install the Go version required by `go.mod`, Git, and Make. Docker is optional
for Valkey integration tests.

```bash
export GITHUB_USER="replace-with-your-github-user"
git clone "https://github.com/${GITHUB_USER}/starport.git"
cd starport
git remote add upstream https://github.com/agentstation/starport.git
make deps
make tools
make check
```

Create a branch from current `main`:

```bash
git fetch upstream main
git switch -c feature/concise-name upstream/main
```

## Choose the owning concept

Put behavior with the concept that owns its invariants:

| Concept | Path |
| --- | --- |
| Canonical inference types | `internal/inference` |
| Starmap projection and acquisition policy | `internal/catalog` |
| Gateway use cases and cache behavior contract | `internal/proxy` |
| Provider runtime lease contract | `internal/providers/connectors` |
| Route planning | `internal/routing` |
| Attempts and retry budgets | `internal/execution` |
| Provider failure mapping | `internal/failure` |
| Request credential placement | `internal/providers/auth` |
| Cloud credential acquisition | `internal/credentials/cloudchain` |
| OpenAI protocol codecs | `internal/protocol/openai` |
| OpenRouter protocol codecs | `internal/protocol/openrouter` |
| Application composition | `internal/app` |
| HTTP wiring | `internal/server` |

Starmap owns provider IDs, model IDs, services, offerings, capabilities,
prices, catalog credentials, and status sources. Change those facts in
Starmap first. Do not add a local provider switch, model list, endpoint table,
or price default to Starport.

Starport owns inference credentials, tenant identity, routing policy,
availability, execution, caching, rate limits, and HTTP protocols.

## Make one coherent change

- Match the local naming and package structure.
- Keep provider model IDs exact and opaque.
- Preserve public behavior unless the change names a new contract.
- Add no legacy provider alias or storage compatibility path.
- Keep side effects at process, storage, transport, or workflow boundaries.
- Update current documentation when behavior changes.

Use direct errors with context. Keep each invariant and state transition with
its owning concept.

## Add tests

Test observable behavior at the changed seam. Cover success, boundaries,
failures, recovery, and concurrency when they apply.

Run one package while you work:

```bash
go test -race ./internal/routing -count=1
```

Run Valkey integration tests when storage, cache, rate-limit, or application
composition behavior changes:

```bash
make test-integration
```

Do not replace a real contract with a mock in the test that must prove that
contract. Do not lower an assertion to hide a defect.

## Format and check

Apply formatting only when you intend to change files:

```bash
make format
```

Run the read-only local gate:

```bash
make check
```

Run the required pull-request commands:

```bash
bash scripts/verify-starmap-ownership.sh
bash scripts/verify-v1-architecture.sh
go test ./...
go vet ./...
make lint
make build
bash scripts/smoke-openrouter-sdks.sh
```

Run `bash scripts/smoke-first-run.sh` when setup, configuration, identity,
diagnosis, or startup behavior changes.

## Update dependencies

Pin the requested module version:

```bash
go get example.com/module@v1.2.3
make tidy
make check
```

Review `go.mod` and `go.sum`. Do not use `@latest` in repository-owned tool
commands.

## Prepare the pull request

Before you commit:

1. Inspect `git status` and `git diff`.
2. Remove unrelated changes.
3. Run the changed-concept tests and repository gates.
4. Record any external check that you could not run as `UNVERIFIED`.

Use a concise commit subject that states the behavior change. Do not add AI
attribution or model co-authorship.

In the pull request, describe:

- The problem and owning concept.
- The observable behavior before and after the change.
- The tests and commands that passed.
- Any remaining risk or unverified platform.

CI must pass on `main` before merge.

## Review documentation changes

Use short procedures and current commands. Verify every relative link. Keep
secret values out of examples, logs, screenshots, and proof files.

Run the strict technical-writing check when that tool is available. The
repository glossary defines project terms such as Starmap, Valkey, and BYOK.

## Ask for help

Use a GitHub issue or discussion for design questions. Include the smallest
reproduction, the exact command, the exit status, and redacted output.
