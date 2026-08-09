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

Implementation commits: `3e1423e`, `8bcc059`, `d945f8c`, `5321b14`,
`1b34093`, `fb68676`, `7b448e5`, and `28b0cab`.

- `internal/setup` owns first-run states, secret generation, local file
  creation, identity creation, and overwrite refusal.
- `starport init --provider openai` reads the OpenAI inference key from its
  named environment variable. It creates the local configuration and the first
  named identity.
- `starport init --provider ollama` enables the local Ollama adapter. It does
  not invent provider or model facts that Starmap owns.
- `starport init --configured-storage` creates the first named identity in the
  configured Badger or Valkey repository.
- Local setup builds the full directory in a sibling staging directory.
  Platform-native no-replace operations install it without replacing another
  directory. Directory synchronization makes the commit durable.
- The configuration file uses mode `0600`, and managed directories use mode
  `0700`.
- The identity repository uses one atomic compare-and-swap batch to claim
  initial setup and store the identity, hash index, and collection record.
- The versioned collection record serializes membership changes. Independent
  key creation retries collection contention, and initial setup proves an
  empty repository in the same transaction.
- `internal/identity.Issuer` owns gateway-key generation, hashing, validation,
  durable creation, and one-time secret return.
- The administrator HTTP controller uses the same issuer.
- Startup requires an existing identity. It does not contain a bootstrap or
  compatibility path.
- The application and setup paths use one storage-configuration projection.
- Failed credential output rolls back only the exact setup snapshot. It
  refuses deletion after any configuration, identity, or application-state
  change.
- An uncertain remote write still returns the candidate key with a nonzero
  exit status. This prevents loss when the remote commit acknowledgment fails.

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
- Atomic collection membership and retry of independent creation contention.
- Claim recovery after deletion of the initial identity.
- Output-failure rollback and refusal after any setup-state change.
- No-replace directory installation and supported-platform compilation.
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

The versioned identity collection and initial-claim recovery contracts also
passed against a new Valkey 7 container.

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

The setup package compiled for Linux amd64, macOS arm64, and Windows amd64.

## Autoreview

The isolated `sol` profile used `gpt-5.6-sol` at high reasoning. TruffleHog
reported a clean bundle on every pass.

The review found lower-priority failure and concurrency cases. Starport
accepted and fixed these findings:

- Loss of the initial credential after output or storage-close failure.
- Ambiguous remote commit acknowledgments.
- Invalid identity names reported as runtime failures.
- A permanent setup claim after deletion of the initial identity.
- Replacement of a concurrently created empty directory.
- Deletion of application state during output rollback.
- Non-atomic repository-emptiness checks.
- Collection-ledger contention between independent key creations.
- Missing directory synchronization after the installed rename.

The convergence review reported no accepted or actionable finding. It rated
the patch correct at 0.99. Its only remaining note requested migration support
for repositories written before this change. Starport rejects that note by
design. The project has not launched, and the repository instructions require
direct breaking changes instead of legacy storage compatibility.

## Pull request gate

Pending.
