# DX5 configuration inspection and diagnosis proof

Date: 2026-08-09

## Fail-before state

- `starport config show` returned an unknown-command usage error and exit
  status 2.
- `starport doctor` returned an unknown-command usage error and exit status 2.
- Operators could not inspect effective configuration or resolved platform
  paths through the CLI.
- Operators could not test configuration, catalog, adapter, storage, or
  identity startup requirements before server construction.

## Implementation

Implementation branch: `codex/starport-dx5-diagnosis`.

- `starport config paths` reports each managed platform path.
- `starport config show` reports the effective configuration. The schema
  replaces every configured secret and URL with `<redacted>`.
- `starport config validate` loads and validates the same configuration as
  `starport serve`.
- Invalid `config validate --json` calls write a stable `valid: false` result
  with a safe loading stage before they return a nonzero status.
- Configuration loading owns stage-only operator errors. The errors preserve
  their causes for programmatic inspection but never show configured values.
- `internal/diagnosis` owns stable startup check identifiers, statuses, and
  messages.
- `starport doctor` checks configuration, the inference credential master key,
  the Starmap catalog, and the configured adapter intersection. It does not
  open storage or use a provider network connection.
- `starport doctor --probe` opens configured storage through a write-blocking
  adapter. It reads the current catalog generation and identity state without
  constructing the server.
- The read-only storage adapter blocks every mutating `KVStore` operation. It
  preserves health checks and read operations.
- Badger read-only open does not create directories or start maintenance. A
  recovery-required database produces an inconclusive check with recovery
  instructions instead of a false failure.
- Platforms where Badger does not support read-only mode also produce an
  inconclusive check with normal-startup guidance.
- Diagnosis uses stable, concept-owned messages. It does not copy dependency
  errors into operator output.
- `internal/providers.Configurations` owns the configuration-to-adapter
  projection. `internal/providers.Availability` owns the activated-adapter
  projection into the Starmap catalog plane.

## Focused verification

Commands:

```bash
go test ./internal/config ./internal/storage ./internal/providers \
  ./internal/diagnosis ./internal/cli ./internal/app ./cmd/starport -count=1
go test -race ./internal/config ./internal/storage ./internal/providers \
  ./internal/diagnosis ./internal/cli ./internal/app ./cmd/starport -count=1
bash scripts/verify-developer-experience.sh
```

Results:

```text
All focused package tests passed.
All focused race tests passed.
Summary: 23 passed, 16 failed
```

All DX5 conditions pass. The 16 verifier failures belong to DX6 through DX8.

The tests cover these contracts:

- Human-readable and JSON configuration output.
- Schema-driven secret and complete URL redaction.
- Safe configuration errors for malformed files and injected dependencies.
- Stable JSON output for failed validation.
- Passive diagnosis without filesystem changes.
- Exact diagnostic check identifiers, statuses, and exit behavior.
- Read-only Badger access and logical write rejection.
- Health-check delegation through the read-only store.
- Badger recovery-required and platform-unsupported states.
- Identity and durable catalog reads through real Badger storage.
- Provider configuration and Starmap availability projections.
- Secret-safe dependency failure messages.

## Real storage and process scenes

A new Badger scene initialized configured storage and ran
`starport doctor --probe`. The report contained seven passing checks, no
failure, and no skipped check. File content, file modes, and the directory tree
were identical before and after the probe. The generated gateway key did not
appear in the report.

A new Valkey 7 container initialized configured storage and ran the same
probe. The report contained seven passing checks. The database contained four
keys before and after the probe. The generated gateway key and configured
Valkey URL did not appear in command output or logs. The test removed the
temporary container.

A passive process scene returned five passing checks and two skipped checks.
It did not create configuration or data files.

## Repository gates

These commands passed:

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
checks. Lint reported zero issues. The SDK smoke suite passed raw chat,
streaming, model, and embedding requests. It also passed the Python,
TypeScript, and Go OpenRouter clients.

Strict technical-writing lint passed the four changed guides with zero
diagnostics. The glossary check reported 15 terms and zero errors.

## Autoreview

The isolated `sol` profile used `gpt-5.6-sol` at high reasoning. TruffleHog
reported a clean bundle.

The all-priority review found and Starport fixed these issues:

- Overlapping configured values could defeat substring-based error redaction.
- The read-only adapter incorrectly blocked the storage health check.
- Raw configuration-loader errors could show configured values.
- URL paths and opaque URL components remained visible.
- Badger recovery requirements produced a false startup failure.
- Short configured values could corrupt diagnostic error messages.
- Badger does not support read-only mode on Windows or Plan 9.
- Unsupported-platform classification ran after the Badger path check.
- Failed JSON validation did not return a machine-readable result.

The fixes replaced dependency-error scrubbing with stable messages, redacted
complete URL values, added safe loader stages, and classified inconclusive
storage probes. The convergence review reported no accepted or actionable
finding and rated the patch correct at 0.88.

## Pull request gate

PR [#81](https://github.com/agentstation/starport/pull/81) merged as
`a6687f1f81206123711a3c36d6d07317c7d06c3f` after all 10 CI checks passed.
