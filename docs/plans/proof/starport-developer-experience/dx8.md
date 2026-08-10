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
- The base Compose file keeps Valkey inside the Compose network.
- The integration override publishes one configurable loopback port.
- Each integration run uses a unique Compose project and uncached Go tests.
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
  list. Its Goldmark syntax parser supports one-file runs without ripgrep.
- The first-run HTTP requests have connection and total time limits.
- The README lists public release archives first. It marks the signed
  Homebrew cask as blocked until Apple release credentials are available.

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
bash scripts/verify-doc-links.sh README.md
```

`make deps` left `go.mod` and `go.sum` unchanged. `make check` reported zero
lint issues and passed the full Go suite. The dedicated integration target
then ran the Valkey storage, cache, application, and repository contracts
against a healthy container. One placeholder reconnection test remains
`UNVERIFIED` because its body requires a manual Valkey restart.

The integration target removed its isolated container, network, and volume
after the tests. Compose configuration also passed with explicit placeholder
values for its two required Starport secrets.

Two integration targets also passed at the same time on host ports `26379`
and `36379`. They used different Compose project names. Both targets removed
their containers, networks, and volumes.

The first-run scene reported:

```text
PASS isolated init, validation, diagnosis, readiness, and authenticated model discovery
```

The developer-experience verifier passed all 41 conditions. The ownership
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

The glossary check passed 15 terms with zero errors. Shellcheck passed the
developer shell scripts. The local link verifier found no broken links in the
current guide set.

Historical architecture records and the standard community rules remain
outside the current-guide lint set. DX8 did not rewrite those records.

## Review fixes

The isolated P2 review found six defects. DX8 fixed all six:

- The integration Valkey port now binds only to `127.0.0.1` on the host.
- Link verification no longer requires an undeclared `rg` command.
- The README does not claim that the blocked Homebrew cask is available.
- Each Valkey test command uses `-count=1`.
- Each integration run uses its shell process ID in the Compose project name.
- Each smoke-test HTTP request has bounded connection and total times.

A second P2 review found two defects in the repaired gates. DX8 also fixed
both defects:

- `format-check` now fails when `gofmt` or `goimports` fails.
- The first-run scene uses an allowlisted environment for each Starport
  process.

The same review exposed two lower-priority boundary defects. Temporary cleanup
now uses the exact directory from `mktemp`. The link verifier now recognizes
angle-bracket destinations, titles, spaces, and balanced parentheses. Its
fixture test proves both valid and missing destinations.

The final P3 review found two more defects. The quick start now carries the
exact built executable through `STARPORT_BIN`. The link verifier preserves a
destination that ends in a balanced parenthesis, and its fixture covers that
case.

A further P3 review found two remaining boundary defects. The base Compose
file no longer exposes the unauthenticated Valkey service. An integration-only
override owns the loopback port. The link verifier now uses a Goldmark syntax
tree instead of regular expressions. Tests cover reference links, inline code,
fenced code, angle destinations, titles, spaces, and balanced parentheses.
The parser resolves source and target symlinks and rejects paths outside the
repository root before file access.
