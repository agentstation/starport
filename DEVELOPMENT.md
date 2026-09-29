# Develop Starport

This guide describes the local build, test, and release-check workflow.

## Requirements

Install these tools:

- The Go version required by `go.mod`.
- Git.
- Make.
- Docker with the Compose plugin for Valkey integration tests.
- `curl`, `jq`, Node.js, npm, and Python for the SDK smoke suite.

Install GoReleaser and Syft only when you create a local release snapshot.

## Set up the repository

```bash
git clone https://github.com/agentstation/starport.git
cd starport
make deps
make tools
make check
```

`make deps` downloads modules without changing `go.mod` or `go.sum`.
`make tools` installs pinned versions of Air, goimports, and golangci-lint.

## Run a local gateway

Use an isolated configuration directory during development:

```bash
export STARPORT_CONFIG_DIR="$PWD/tmp/config"
export OPENAI_API_KEY="replace-with-provider-inference-key"
go run ./cmd/starport init
go run ./cmd/starport doctor --probe
go run ./cmd/starport serve
```

`STARPORT_CONFIG_DIR` must be an absolute path. It changes all managed local
paths together.

For automatic rebuilds, install the pinned tools and run:

```bash
make tools
make dev
```

Air builds into `tmp/` and sends an interrupt before restart. The normal CLI
still requires the explicit `serve` command.

## Make targets

Run `make help` for the current list.

| Target | Result | Changes tracked files |
| --- | --- | --- |
| `make build` | Builds `./starport` | No |
| `make run` | Builds and starts `starport serve` | No |
| `make test` | Runs all Go tests | No |
| `make test-race` | Runs all Go tests with race detection | No |
| `make test-integration` | Runs Valkey integration tests | No |
| `make format-check` | Checks gofmt and goimports output | No |
| `make lint` | Runs the pinned golangci-lint version | No |
| `make verify` | Runs architecture and release contracts | No |
| `make check` | Runs formatting, lint, test, and verifier gates | No |
| `make format` | Applies the pinned goimports version | Yes |
| `make tidy` | Updates Go module metadata | Yes |
| `make release-snapshot` | Builds and checks release artifacts | No |

`make check` does not format code or tidy modules. This property keeps local
and CI checks deterministic.

## Test behavior

Run one package:

```bash
go test -race ./internal/routing -count=1
```

Run one test:

```bash
go test ./internal/server -run TestName -count=1
```

Run the isolated first-start scene:

```bash
bash scripts/smoke-first-run.sh
```

The scene creates a temporary configuration root. It initializes a gateway
identity, validates configuration, probes storage, starts the server, and
reads the authenticated model catalog.

Run the OpenRouter compatibility scene:

```bash
bash scripts/smoke-openrouter-sdks.sh
```

This scene tests raw HTTP plus the pinned official Python, TypeScript, and Go
OpenRouter clients.

## Native application tests

Windows AMD64 runs the application suite in four concurrent CI jobs.
Other native platforms keep one complete application run.
Production storage settings and test assertions remain the same on every platform.

Each shard discovers runnable tests, examples, and fuzz seed owners with Go's race build.
A stable name hash assigns each owner to one shard.
New owners enter the next run automatically. Subtests and fuzz seeds stay with their owner.

The aggregate gate checks complete discovery, exact execution counts, source identity, and native toolchain agreement.
Build requires every shard and that gate.
Optional service skips remain visible and do not qualify a required native contract.
Linux coverage and the other native checks remain unchanged.

Reproduce one shard with a fresh output directory:

```bash
python scripts/app_shards.py run --index 0 --output /tmp/starport-app-shard-0
```

The script gives cold race compilation a separate 15-minute budget.
Each shard has a 12-minute Go test deadline.
The artifact retains the discovery roster, raw Go JSON events, and native toolchain details.
It does not combine four package completions into one result.

`scripts/native_catalog.py` binds the additional artifacts when it captures current CI evidence.
Its verifier also reads historical captures from the original complete suite.

## Run storage integration tests

```bash
make test-integration
```

The target uses a separate Compose file and a unique project. It runs Valkey
on loopback port `16379` and PostgreSQL on loopback port `15432`. A shell trap
removes these test containers, their network, and volumes when the command ends.
Set `POSTGRES_INTEGRATION_PORT` to change the PostgreSQL port.

Set `VALKEY_INTEGRATION_PORT` to use another host port:

```bash
make test-integration VALKEY_INTEGRATION_PORT=26379
```

For the complete local Compose environment, use:

```bash
make dev-docker
make dev-docker-logs
make dev-docker-stop
```

`make dev-docker-clean` also removes the Compose volumes. Use that target only
when you intend to remove the local application data. Initialize the local
recipe first through the [container procedure](docs/OPERATOR-GUIDE.md#container-start).

Test the actual container paths, fresh startup, recreation, and cold restore:

```bash
docker build -t starport-storage-recipe:local .
STARPORT_RECIPE_IMAGE=starport-storage-recipe:local go test -race -count=1 \
  -timeout 5m ./internal/config -run '^Test(ComposeStorageRecipes|ContainerRecipePersistence)$'
```

Build the image from the same tree as the test. The probe uses temporary
credentials, fresh volumes, and an offline catalog. It verifies authenticated
KV records, SQLite audit records, uploaded bytes, and catalog access after both
container recreation and a cold backup restore. It deletes only its own Compose
project. The CI Storage Recipes job runs this check and retains its evidence.
The check does not qualify host power loss or populated fleet recovery.

## Format and lint

Check formatting without changes:

```bash
make format-check
```

Apply formatting:

```bash
make format
```

Run the repository lint configuration:

```bash
make lint
```

Do not lower a lint rule or test assertion to hide a defect.

## Respect concept ownership

Starport uses concept-owned boundaries:

- `internal/inference` owns canonical inference types.
- `internal/catalog` projects one immutable Starmap generation.
- `internal/routing` owns deterministic route planning.
- `internal/execution` owns attempts and retry budgets.
- `internal/failure` normalizes provider failures.
- `internal/providers/auth` owns request credential placement.
- `internal/credentials/cloudchain` owns renewable cloud credential acquisition.
- `internal/protocol/openai` owns OpenAI protocol codecs.
- `internal/protocol/openrouter` owns OpenRouter protocol codecs.
- `internal/app` composes the concepts.
- `internal/server` wires HTTP routes.

Starmap owns provider IDs, model IDs, services, offerings, capabilities,
prices, catalog credentials, and status sources. Do not add a provider switch,
model list, endpoint table, or price default to Starport.

Starport owns inference credentials, account identity, routing policy,
availability, execution, caching, rate limits, and HTTP protocols.

## Add provider inference support

First add or update the provider facts in Starmap. Publish a Starmap release,
then update the Starport module version.

In Starport:

1. Add the transport adapter under `internal/providers`.
2. Project required configuration from the Starmap adapter descriptor.
3. Put request authentication in `internal/providers/auth` when needed.
4. Put renewable cloud credential acquisition in `internal/credentials/cloudchain`.
5. Add contract tests for requests, responses, streaming, and failures.
6. Prove that the Starmap ownership verifier still passes.

Keep provider model IDs exact and opaque.

## Change dependencies

Add or update one dependency explicitly:

```bash
go get example.com/module@v1.2.3
make tidy
make check
```

Review both module files. Do not use an unpinned tool version in the Makefile
or a workflow.

## Build a release snapshot

Install the exact GoReleaser and Syft versions from the workflow, then run:

```bash
make release-check
make release-snapshot
```

The snapshot checks five target binaries, five archives, five SBOMs, shell
completions, the manual page, and the generated Homebrew cask. Snapshot mode
skips Apple signing and notarization. A stable release signs and notarizes
macOS binaries when all Apple release credentials are available. The release
does not require these credentials for the CLI cask. On macOS, the cask
removes `com.apple.quarantine` only from its staged Starport binary. It does
not change other user paths or run this hook on Linux.

## Required pull-request gates

Run these commands before a pull request:

```bash
bash scripts/verify-starmap-ownership.sh
bash scripts/verify-v1-architecture.sh
go test ./...
go vet ./...
make lint
make build
bash scripts/smoke-openrouter-sdks.sh
```

Also run tests for each changed concept. Report every skipped external check
as `UNVERIFIED`.

## Troubleshoot startup

Inspect managed paths and safe effective values:

```bash
starport config paths
starport config show
starport config validate
starport doctor --probe
```

`config show` hides secret and URL values. `doctor` does not write storage.

If a test leaves a local server or Compose service, stop that process before
you run the scene again. Do not remove a configuration directory until you
confirm its exact path with `starport config paths`.
