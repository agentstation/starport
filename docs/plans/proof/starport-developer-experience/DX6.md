# DX6 remove init, add auth bootstrap

Plan: `docs/plans/starport-developer-experience-plan.html`, task DX6.
Base commit: `00fbf79dfa57f37ed58c620ceb5bc860cf882a4f` (origin/main).
Branch: `codex/dx-DX6`. Go from `go.mod` (go1.27.2), macOS.

This file masks every API key. It shows only a SHA-256 prefix of a key.
Paths under the scratch root show as `<scratch>`.

## Fail-before

Commit `d1fbdfb7` adds `TestAuthBootstrapClaimsTheFirstKey` before any
behavior change.

```sh
go test ./cmd/starport/ -run TestAuthBootstrapClaimsTheFirstKey -count=1 -v
```

```text
=== RUN   TestAuthBootstrapClaimsTheFirstKey
    auth_bootstrap_test.go:22: exit code = 2, want 0; stderr = flag provided but not defined: -name
--- FAIL: TestAuthBootstrapClaimsTheFirstKey (0.00s)
FAIL
FAIL	github.com/agentstation/starport/cmd/starport	0.883s
```

The base `auth` command has no `bootstrap` subcommand. It reads `bootstrap` as
an argument and refuses the `--name` flag. A binary built from the base commit
shows the same refusal from the shell:

```text
$ <scratch>/starport-base auth bootstrap
unknown auth command "bootstrap"
exit=2
$ <scratch>/starport-base auth bootstrap --name primary-admin
flag provided but not defined: -name
exit=2
```

### Verifier check DX-CLI-3

The run of `scripts/verify-developer-experience.sh` came after the removal of
the `init` command from `internal/cli/app.go` and before the change to the
check. `DX-CLI-3` failed:

```text
FAIL DX-CLI-3
Summary: 46 passed, 1 failed
exit=1
```

The check now reads `require_text DX-CLI-3 'Name:.*"bootstrap"'
internal/cli/auth.go`. After the change, the result is
`Summary: 47 passed, 0 failed`.

## Observation: the built binary

```sh
go build -o <scratch>/starport-dx6 ./cmd/starport
env -i PATH="$PATH" HOME=<scratch>/home STARPORT_HOME=<scratch>/home/sp \
  STARPORT_CATALOG_NETWORK_MODE=offline <scratch>/starport-dx6 <args>
```

| Arguments | Result |
| --- | --- |
| `init` | `unknown command "init"`. Exit status 2. |
| `auth bootstrap` | `starport auth bootstrap requires --name or --recover`. Exit status 2. |
| `auth bootstrap --name a --recover` | `starport auth bootstrap accepts --name or --recover, not both`. |
| `auth bootstrap --recover` on an empty root | `no interrupted local setup to recover: the local root is empty; run "starport serve" to initialize it`. Exit status 1. The command creates no root directory. |
| `auth bootstrap --name primary-admin` on an empty local root | `the first run of "starport serve" initializes this local root; "starport auth bootstrap --name" is for configured shared storage`. Exit status 1. |
| `auth bootstrap --name primary-admin --json` with a master key and `STARPORT_STORAGE_BADGER_PATH` | Exit status 0. The JSON has the fields `api_key` and `api_key_name` (`primary-admin`). Key SHA-256 prefix `59ba7fbab02f`. |
| A second `auth bootstrap --name second-admin` on the same store | `starport is already initialized`. Exit status 1. Standard output is empty. |
| `--help` | The command list shows `auth` and no `init`. The command creates no root directory. |

## Commands and results

| Command | Result |
| --- | --- |
| `go test -count=1 -json ./internal/cli/ ./internal/setup/ ./internal/diagnosis/ ./cmd/starport/` | 4 packages pass. 465 tests and subtests pass, 0 skip, 0 fail. Exit status 0. |
| `go test ./internal/config/ -count=1` | Pass. |
| `go test -timeout 30m -count=1 -json ./...` | Exit status 0. 69 packages pass, 11 have no test files, 0 fail. 7901 tests and subtests pass, 718 skip, 0 fail. |
| `gofmt -l` on each changed and new Go file | No output. |
| `go vet ./...` | Exit status 0. |
| `make docs-generate && git status --short` | `PASS wrote 6 reference files to docs/site/generated`. No diff. |
| `scripts/smoke-first-run.sh` | `PASS ephemeral dev, first-run serve, validation, diagnosis, restart, readiness, and authenticated model discovery`. Exit status 0. |
| `scripts/verify-developer-experience.sh` | `Summary: 47 passed, 0 failed`. Exit status 0. |
| `shellcheck scripts/smoke-first-run.sh scripts/verify-developer-experience.sh` | No output. Exit status 0. |
| `scripts/test-readme-quickstart-verifier.sh` | `PASS README quickstart verifier regression tests`. The new `removed-init` fixture fails for the reason `README contains an obsolete binary variable or the removed init command`. |
| `shellcheck` on `scripts/verify-readme-quickstart.sh` and `scripts/test-readme-quickstart-verifier.sh` | 2 and 3 `SC2016` notes. The base commit has the same counts. |
| `python3 -m py_compile scripts/test-storage-recipes.py scripts/readme-demo/capture.py` | Exit status 0. |
| `pnpm install --frozen-lockfile && pnpm test` in `website/` | 4 files and 84 tests pass. |
| `pnpm install --frozen-lockfile && pnpm exec vitest run src/components/settings/AuthModeControl.test.tsx` in `console/` | 1 file and 5 tests pass. |

Of the 718 skipped tests, 446 have a Valkey, MySQL, or PostgreSQL subtest name.
Those tests need an external store that this run did not supply.

`scripts/test-storage-recipes.py` now calls `auth bootstrap --name`. That
script needs Docker and Compose. This task did not run it.

## Documents

Each changed Markdown file passes `technical-writing lint --mode strict`
with no new diagnostics:

| File | Base commit | Branch |
| --- | --- | --- |
| `README.md` | 0 | 0 |
| `DEVELOPMENT.md` | 0 | 0 |
| `docs/ARCHITECTURE.md` | 26 | 26, the same set |
| `docs/FLEET_INITIALIZATION.md` | 0 | 0 |
| `docs/OPERATOR-GUIDE.md` | 68 | 68, the same set |
| `docs/site/operate-starport/fleet.md` | 0 | 0 |
| `docs/site/start/keys-and-roles.md` | 0 | 0 |
| `docs/site/start/local-persistent.md` | 0 | 0 |
| `docs/site/start/temporary-development.md` | 0 | 0 |
| `docs/site/storage/caches.md` | 0 | 0 |
| `internal/config/README.md` | 0 | 0 |
| `skills/starport/SKILL.md` | 0 | 0 |

## The remaining `starport init` lines

`git grep -n 'starport init'` matches these files only:

- `docs/assets/first-use-v1.2.0/capture.py` and the `events.json` files for
  v1.2.0 and v1.3.0: recorded release transcripts.
- `docs/proof/readme-demo/rehearsal-2026-10-04/events.json`: a recorded
  transcript.
- `docs/plans/proof/starport-developer-experience/DX0.md` and `DX2.md`:
  historical proof.
- `docs/plans/starport-developer-experience-plan.html` and
  `docs/plans/README.md`: the plan, which the orchestrator owns.

## Named tests

| Test | Package | Result |
| --- | --- | --- |
| `TestAuthBootstrapClaimsTheFirstKey` | `cmd/starport` | pass |
| `TestAuthBootstrapRefusesASecondKey` | `cmd/starport` | pass |
| `TestAuthBootstrapRecoverRefusesCleanRoot` | `cmd/starport` | pass |
| `TestAuthBootstrapRecoversPartialSetup` (3 subtests) | `internal/setup` | pass |
| `TestNoArgumentsShowHelp` | `internal/cli` | pass |
| `TestInitIsAnUnknownCommand` | `internal/cli` | pass |

Supporting tests:

- `cmd/starport`: `TestAuthBootstrapNameRefusesAFirstRunRoot`,
  `TestRunContextAuthBootstrapDelegatesToInitializer`.
- `internal/cli`: `TestAuthBootstrapUsesInjectedRunnerAndJSONOutput`,
  `TestAuthBootstrapTextOutputNamesServe`, `TestAuthBootstrapUsageErrors`,
  `TestAuthBootstrapRollsBackWhenCredentialOutputFails`,
  `TestAuthBootstrapReturnsCredentialBeforePostCommitFailure`,
  `TestAuthBootstrapRecoverPrintsTheRetriedKey`,
  `TestAuthBootstrapRecoverReportsASettledSetup`,
  `TestAuthBootstrapRecoverFailureIsRuntimeError`.
- `internal/setup`: `TestRecoverRefusesAnEmptyRootAndWritesNothing`,
  `TestRecoverRefusesAReadyRoot`,
  `TestRecoverRefusesAPartialRootWithoutATransaction`.

All pass.

`TestAuthBootstrapRecoversPartialSetup` is in `internal/setup`, not in
`cmd/starport`. A real pending transaction comes only from a child process
that stops at a setup checkpoint, and the checkpoint is internal to `setup`.
The test reuses the crash helper of `TestSetupRecoversProcessTermination`. It
covers two unpublished phases (`prepared`, `data-published`), where recovery
prints a new key. It also covers one published phase
(`configuration-published`), where recovery settles the root and prints no
key. The command composition sends `--recover` directly to `Service.Recover`.

## Design

- `--recover` calls `setup.Service.Recover`. It inspects the root first, so
  an empty root gets no writes. `GuardLocalStorage` then finds an incomplete
  setup transaction. Only on `ErrPartialState` does `Recover` retry
  through `Initialize`, which settles the journal before it creates a key.
  An empty, ready, or untouched-partial root gets `ErrNoPendingSetup`.
- A retry of a published transaction returns no key. The command then prints
  that recovery is complete. It tells the operator to start `serve` and to
  create a key in the console.
- `--name` refuses a root that `setup.FirstRun` accepts. A key in that store
  without the setup transaction leaves a partial root that `serve` refuses.
- `DefaultAPIKeyName` moved from `internal/cli/init.go` to `internal/setup`,
  beside `RecoverCommand`. The setup package owns the name of the first local
  key.
- The `Initializer`, `InitOptions`, and `InitResult` names stay, to keep the
  change small. `InitOptions` now holds `APIKeyName` and `Recover`.
- A local root has no custom first-key name now. The README, the operator
  guide, and the site name `local-admin` and point to the console for more
  keys.
