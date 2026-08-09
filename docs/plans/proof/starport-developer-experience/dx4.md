# DX4 local setup proof

Date: 2026-08-09

## Fail-before state

- Empty identity storage required a manually selected bootstrap secret.
- The process converted that temporary secret into a wildcard identity during
  startup.
- An operator had to start Starport, call the administrator API, save another
  key, stop Starport, remove the temporary secret, and start Starport again.
- Local configuration depended on manual file edits and two manual secrets.
- Identity creation duplicated credential generation and hashing in the HTTP
  controller.

## Implementation

Work commit: `3e1423e411cd0a66801f3e59cba2b9556d338741`

- `internal/setup` owns first-run states, secret generation, local file
  creation, identity creation, and overwrite refusal.
- `starport init --provider openai` reads the OpenAI inference key from its
  named environment variable. It creates the local configuration and the first
  named identity.
- `starport init --provider ollama` enables the local Ollama adapter. It does
  not invent provider or model facts that Starmap owns.
- `starport init --configured-storage` creates the first named identity in the
  configured Badger or Valkey repository.
- Local setup builds the full directory in a sibling staging directory and
  installs it with one rename. Concurrent attempts have one winner.
- The configuration file uses mode `0600`, and managed directories use mode
  `0700`.
- The identity repository uses one atomic compare-and-swap batch to claim
  initial setup and store the identity and hash index.
- `internal/identity.Issuer` owns gateway-key generation, hashing, validation,
  durable creation, and one-time secret return.
- The administrator HTTP controller uses the same issuer.
- Startup requires an existing identity. It does not contain a bootstrap or
  compatibility path.
- The application and setup paths use one storage-configuration projection.

The gateway key keeps the established UUID-key wire format. The issuer uses
independent `crypto/rand` input because the dependency generator uses shared
mutable hash state. This change removes that race without changing the key
format.

## Focused verification

Commands:

```bash
go test ./internal/identity ./internal/setup ./internal/cli \
  ./internal/config ./internal/app ./cmd/starport \
  ./internal/server/controllers -count=1
go test -race ./internal/setup ./internal/identity ./internal/cli \
  ./internal/app ./cmd/starport -count=1
bash scripts/verify-developer-experience.sh
```

Results:

```text
All focused package tests passed.
All focused race tests passed.
Summary: 21 passed, 18 failed
```

All four DX4 conditions pass. The 18 verifier failures belong to DX5 through
DX8.

The tests cover these contracts:

- OpenAI and Ollama profile creation.
- Owner-only file mode and plaintext-key exclusion from stored state.
- Existing, partial, and concurrent local setup.
- Atomic initial identity claims under concurrency.
- Parseable gateway-key output.
- Badger and Valkey storage projection.
- CLI usage errors, JSON output, and the configured-storage form.
- Startup refusal when identity storage is empty.

## Real storage and process scenes

A real Valkey 7 container passed the storage and application integration
tests. The application test first exposed an invalid fixture: the test replaced
the seeded memory store with Valkey but did not seed Valkey. The corrected test
creates its fixture through the real identity repository and passes on repeated
runs.

The configured-storage scene proved this sequence against a new Valkey
container:

1. `starport init --configured-storage --name valkey-admin --json` returned one
   gateway key.
2. A second initialization refused the existing repository.
3. `starport serve` started with the same storage and reported ready.
4. The temporary container was removed.

The OpenAI local scene proved configuration creation, mode `0600`, one-time
JSON output, overwrite refusal, and ready server startup. The test-created
macOS platform directory was moved into the temporary evidence directory after
the scene. The standard Starport configuration path is clean.

The Ollama contract test proves local state creation. Current Starmap owns the
Ollama service metadata but does not publish arbitrary installed model links.
The CLI and guides require a reviewed Starmap workspace before server startup.

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
checks. Lint reported zero issues. The SDK smoke suite passed raw HTTP and the
Python, TypeScript, and Go OpenRouter clients.

Strict technical-writing lint passed all seven changed guides with zero
diagnostics. The glossary check reported 15 terms and zero errors.

## Autoreview

Pending.

## Pull request gate

Pending.
