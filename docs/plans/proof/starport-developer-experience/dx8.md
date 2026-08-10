# DX8 developer tools and documentation proof

Date: 2026-08-09

## Fail-before state

The baseline developer workflow had these defects:

- `make check` changed Go source through its `format` dependency.
- `make deps` ran `go mod tidy` and could change module files.
- Tool installation used unpinned `@latest` versions and an old Air module path.
- Make targets used the retired `docker-compose` command.
- The integration target did not clean up Valkey after a test failure.
- The Compose file did not publish the Valkey port that the tests used.
- README and developer guides named stale targets and incomplete procedures.
- The cache-control guide duplicated provider facts that Starmap owns.

The first developer-experience verification run passed 34 conditions and
failed these five conditions:

```text
DX-DEV-1 Docker Compose command
DX-DEV-3 read-only check target
DX-DOC-1 Homebrew install command
DX-DOC-2 current documentation index
DX-DOC-3 first-run smoke scene
```

The first repaired integration run found two additional baseline defects. The
Compose parser first required unrelated Starport secrets. After test-only
values supplied those fields, the tests could not connect because the Compose
file did not publish the Valkey port.

## Implementation

Implementation branch: `codex/starport-dx8-dev-docs`.

- The Makefile pins Air and goimports. It has no `@latest` tool versions.
- `make check` and `make check-race` use `format-check` and do not change
  tracked files.
- `make deps` downloads modules. The explicit `make tidy` target owns module
  metadata changes.
- All Compose targets use the Docker Compose plugin.
- `make test-integration` uses an isolated Compose project and waits for
  Valkey health. It supplies test-only configuration. A shell trap removes
  its container, network, and volume.
- The Compose file publishes a configurable Valkey host port.
- `STARPORT_CONFIG_DIR` gives first-run tests an explicit and isolated
  configuration root. The resolver rejects relative paths.
- The first-run scene builds Starport and initializes an identity. It validates
  and diagnoses the configuration. It then starts the server, checks
  readiness, and reads the authenticated OpenRouter model catalog.
- The current README, development guide, contribution guide, documentation
  index, and prompt-cache guide describe tested behavior.
- The prompt-cache guide derives capability and price facts from Starmap. It
  does not list hard-coded provider support.
- The document-link verifier checks every current guide from one canonical
  list.

## Developer workflow verification

These commands passed:

```bash
make deps
make format-check
make check
make test-integration
bash scripts/smoke-first-run.sh
bash scripts/verify-developer-experience.sh
bash scripts/verify-doc-links.sh
```

`make deps` left `go.mod` and `go.sum` unchanged. `make check` reported zero
lint issues and passed 34 Go packages. The JSON test report counted 1,075
passed tests and nine skipped Valkey tests in the standard suite. The
dedicated integration target then ran the Valkey storage, cache, application,
and repository contracts against a healthy container. One placeholder
reconnection test remains `UNVERIFIED` because its body requires a manual
Valkey restart.

The integration target removed its isolated container, network, and volume
after the tests. Compose configuration also passed with explicit placeholder
values for its two required Starport secrets.

The first-run scene reported:

```text
PASS isolated init, validation, diagnosis, readiness, and authenticated model discovery
```

The developer-experience verifier passed all 39 conditions. The ownership
verifier passed 12 checks, and the architecture verifier passed 12 checks.
The release verifier passed 14 checks. The release workflow and documentation
link contracts also passed.

## Documentation verification

Strict technical-writing lint passed these 12 current guides with zero
diagnostics:

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
```

The glossary check passed 15 terms with zero errors. Shellcheck passed both
new shell scripts. The local link verifier found no broken links in the
current guide set.

Historical architecture records and the standard community rules remain
outside the current-guide lint set. DX8 did not rewrite those records.
