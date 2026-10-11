# DX2 first-run initialization in serve

Plan: `docs/plans/starport-developer-experience-plan.html`, task DX2.
Base commit: `c027e2b3200f3147d2bab9bdce053f72224ce87c` (origin/main).
Branch: `codex/dx-DX2`. Go from `go.mod` (go1.27.2), macOS.

This file masks every API key. A key shows only its first 6 characters. Paths
under the scratch root show as `<scratch>`.

## Fail-before

Commit `9ae40616` adds `TestServeInitializesEmptyStorage` and
`TestServeGreetsAfterInitialization` before any behavior change.

```sh
go test ./cmd/starport/ -run 'TestServeInitializesEmptyStorage|TestServeGreetsAfterInitialization' -count=1 -v
```

```text
=== RUN   TestServeInitializesEmptyStorage
    serve_first_run_test.go:29: exit code = 1, want 0; stderr = create application: provider credential master key is required
--- FAIL: TestServeInitializesEmptyStorage (0.00s)
=== RUN   TestServeGreetsAfterInitialization
    serve_first_run_test.go:52: serve greeted before initialization wrote the configuration
    serve_first_run_test.go:55: exit code = 1, want 0; stderr = create application: provider credential master key is required
--- FAIL: TestServeGreetsAfterInitialization (0.00s)
FAIL
FAIL	github.com/agentstation/starport/cmd/starport	0.909s
```

The first test fails with the F2 error. The second test also records that the
greeting came before the configuration file existed (F9).

The new README pin fails on the README before the document change. The run
used the commit `6aff616f` tree with only the new `readme_test.go`:

```text
--- FAIL: TestReadmeTemporaryPathPrecedesPersistentPath (0.00s)
    readme_test.go:188: missing claim "For persistent local state, run `starport serve`."
    readme_test.go:188: missing claim "The first identity has the name `local-admin`."
FAIL
```

The new smoke script fails on an origin/main export. Serve greets, and then it
stops with the F2 error:

```text
first-run server did not become ready
...
create application: provider credential master key is required
exit=1
```

## Observation: `serve` twice on an empty home

```sh
go build -o <scratch>/starport ./cmd/starport
env -i PATH="$PATH" HOME="$HOME" STARPORT_HOME=<scratch>/home STARPORT_SERVER_PORT=18483 \
  STARPORT_CATALOG_NETWORK_MODE=offline STARPORT_CATALOG_ACQUISITION_ENABLED=false \
  tools/run_for.py 8 run1.out <scratch>/starport serve
# The same command again writes run2.out.
```

Run 1, human lines only (Badger and JSON log lines removed):

```text
Initialized Starport.
Configuration: <scratch>/home/config/config.env
Data: <scratch>/home/data
Gateway API key (shown once): STARPO<masked>

Welcome to Starport.

  starport ui        Open the console. No key to paste: the link signs
                     this browser in with a session from this machine.
  starport auth url  Print that link instead of opening it.

A gateway API key is for a program calling the gateway, not for the
console. Create one in the console under Keys, or with `starport init`.

[run_for] exited_early=False exit=0
```

Run 2 prints no key and no welcome. It exits with status 0 after SIGINT.

Files under the home after both runs (depth 2):

```text
config/.record-publications
config/.starport-setup
config/config.env
data/.starport-setup-badger
data/badger
data/catalog
data/files
data/local-admin-token.json
data/local-admin-token.json.lock
data/sqlite
data/welcomed
state/catalog
state/credentials
```

The key prints before the welcome. The local admin token is on disk. The
welcome text still names `starport init`. DX6 owns that text.

## Commands and results

| Command | Result |
| --- | --- |
| `go test ./internal/cli/ ./internal/setup/ ./internal/app/ ./internal/localauth/ ./cmd/starport/` | The default 10-minute timeout stops `internal/app` at 602 s. All other packages pass. |
| `go test -timeout 30m ./internal/cli/ ./internal/setup/ ./internal/app/ ./internal/localauth/ ./internal/config/ ./cmd/starport/` | All 6 packages pass. `internal/app` takes 865 s. Exit status 0. |
| `go test ./internal/setup/ -run 'TestInitializeConcurrentSingleWinner\|TestFirstRun\|TestInitializeFirstRun' -count=1 -v` | 4 tests pass. `TestInitializeConcurrentSingleWinner` passes. |
| `go test ./cmd/starport/ ./internal/cli/ -run TestServe -count=1 -v` | `cmd/starport`: 7 tests and 5 subtests pass. `internal/cli`: 6 tests pass. |
| `go test -timeout 30m -json ./...` (the `make test` timeout) | 69 packages pass, 11 packages have no test files, 0 fail. 7,878 tests and subtests pass, 718 skip, 0 fail. `internal/app` takes 1,275 s. Exit status 0. |
| `gofmt -l` on each changed and new Go file | No output. |
| `go vet ./...` | Exit status 0. |
| `make docs-generate && git status --short` | `PASS wrote 6 reference files`. No diff. |
| `STARPORT_SMOKE_PORT=18482 scripts/smoke-first-run.sh` | `PASS ephemeral dev, first-run serve, validation, diagnosis, restart, readiness, and authenticated model discovery`. Exit status 0. |
| `shellcheck scripts/smoke-first-run.sh` | No output. Exit status 0. |
| `technical-writing lint README.md --mode developer` | 0 diagnostics. |
| `technical-writing lint skills/starport/SKILL.md --mode developer` | 0 diagnostics. |
| `technical-writing lint docs/OPERATOR-GUIDE.md --mode developer` | 68 diagnostics. origin/main also has 68. The changed lines add none. |

## Named tests

| Test | Package | Result |
| --- | --- | --- |
| `TestServeInitializesEmptyStorage` | `cmd/starport` | pass |
| `TestServeGreetsAfterInitialization` | `cmd/starport` | pass |
| `TestServeReloadsConfigurationAfterInitialization` | `cmd/starport` | pass |
| `TestServeProvisionsMachineToken` | `cmd/starport` | pass |
| `TestServeRollsBackWhenKeyOutputFails` | `cmd/starport` | pass |
| `TestServeKeepsPartialStateError` | `cmd/starport` | pass |
| `TestServeSkipsInitializationOnConfiguredStorage` (5 subtests) | `cmd/starport` | pass |
| `TestServeDeliversTheFirstRunKeyBeforeTheWelcome` | `internal/cli` | pass |
| `TestServeOutputRollsBackWhenTheKeyWriteFails` | `internal/cli` | pass |
| `TestServeDoesNotGreetAFailedStart` | `internal/cli` | pass |
| `TestInitializeConcurrentSingleWinner` | `internal/setup` | pass |

This task does not change the `internal/app` startup table. The `internal/app`
test changes add the new `ServerOutput` argument to the injected server runners
only.

## Readiness deadline on the CI runners

The first pull request run (`38087337098`) failed `TestServeInitializesEmptyStorage`
on `macos-15` with `gateway did not become ready; exit code = 0`. The test
waited 60 s for `/health/ready`. The HTTP server started at 61 s, after the
cancel. The same first start took 35.7 s on `ubuntu-24.04`, 30.3 s on
`ubuntu-24.04-arm`, and 60.4 s on `windows-2025`, all under the race
detector. The second start in the same package took 18 s to 39 s. Locally,
without the race detector, the first start takes 3 s.

The fix raises the readiness deadline to 5 minutes through `readyDeadline`.
The poll returns as soon as the gateway is ready, so a passing run pays
nothing extra.
