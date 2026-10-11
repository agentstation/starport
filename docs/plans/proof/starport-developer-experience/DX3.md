# DX3 persistent `starport dev`

Plan: `docs/plans/starport-developer-experience-plan.html`, task DX3.
Base commit: `00fbf79dfa57f37ed58c620ceb5bc860cf882a4f` (origin/main).
Branch: `codex/dx-DX3`. Go from `go.mod` (go1.27.2), macOS.

This file shows no API key, master key, or launch token. A secret shows only
as the first 12 hexadecimal characters of its SHA-256 digest. Paths under the
scratch root show as `<scratch>`.

## Fail-before

Commit `968fee17` adds `TestDevPersistsAcrossRuns` and
`TestDevSecondRunPrintsNoKey` before any behavior change.

```sh
go test ./cmd/starport/ -run TestDevPersistsAcrossRuns -count=1 -v
go test ./internal/cli/ -run TestDevSecondRunPrintsNoKey -count=1 -v
```

Test lines only (JSON and Badger log lines removed):

```text
=== RUN   TestDevPersistsAcrossRuns
    dev_test.go:54: run 2 minted a second key: run 1 key sha256 54e166d53a8c, run 2 key sha256 a77a3fea4ace
    dev_test.go:58: run 2 answered the run 1 key and file with status 401, want 200
    dev_test.go:61: run 1 wrote no master key to the platform configuration file
--- FAIL: TestDevPersistsAcrossRuns (4.33s)
FAIL	github.com/agentstation/starport/cmd/starport	5.819s
=== RUN   TestDevSecondRunPrintsNoKey
    development_test.go:27: run dev: development session is invalid
--- FAIL: TestDevSecondRunPrintsNoKey (0.00s)
FAIL	github.com/agentstation/starport/internal/cli	0.622s
```

The first test shows two different key digests, a 401 for the run 1 key and
file, and no master key on disk. The log shows both runs in a
`starport-dev-` scratch directory. The second test shows that the old session
contract required a plaintext key.

## Change

- `config.LoadPersistentDevelopment` reads the same sources as `starport serve`
  and keeps the platform paths and the port. Its profile sets the host to
  `127.0.0.1`, the log output to standard output, and TLS to off. The profile
  runs after the caller overrides, so no flag widens it.
- The profile refuses shared or external management. It also refuses storage
  away from the platform roots. The refusal comes before validation and before
  any store opens. The refused
  settings are `STARPORT_CONFIG_MANAGEMENT`, `STARPORT_STORAGE_MODE`, the Badger
  path, `STARPORT_STORAGE_SQL_MODE`, the SQLite path, `STARPORT_FILES_BACKEND`,
  the files path, and `STARPORT_CACHE_BACKEND`. The error names each setting,
  `starport serve`, and `starport dev --ephemeral`.
- Persistent dev runs the serve preparation as `prepareGateway` in
  `cmd/starport/serve.go`. The steps are load, the DX2 first-run initialization
  with key delivery and rollback, reload, and the machine token.
- The development session carries the authentication state and an optional new
  key. The key line prints only when the run minted a key. The URL, the
  authentication state, and the console link print on every run.
- `starport dev --ephemeral` keeps the old scratch profile. The smoke script,
  the native release check, and the demo capture scripts use it. The release
  check and the README capture read `dev --help` first, so they still run an
  older binary without the flag.
- The README, the operator guide, the agent skill, `.env.example`, and the
  documentation site now name `starport dev --ephemeral`. Each changed line
  describes the temporary gateway. DX5 owns the full narrative change.

## Acceptance with the real binary

The driver `accept.py` in the scratch root runs the binary through
`tools/run_for.py` for 30 s per run. The command output goes through a FIFO.
The driver keeps the key in memory and writes only redacted lines.

```sh
go build -o <scratch>/dx3-bin/starport ./cmd/starport
env -i PATH="$PATH" HOME="$HOME" STARPORT_HOME=<scratch>/dx3-accept/home STARPORT_SERVER_PORT=18531 \
  STARPORT_CATALOG_NETWORK_MODE=offline STARPORT_CATALOG_ACQUISITION_ENABLED=false \
  tools/run_for.py 30 <fifo> <scratch>/dx3-bin/starport dev --no-open
# The same command again for run 2.
```

| Check | Run 1 | Run 2 |
| --- | --- | --- |
| Exit after SIGINT | 0 | 0 |
| Key lines printed | 1, sha256 `72b80a9d4e42` | 0 |
| Master key in `config/config.env` | sha256 `607ebfe3b07f` | sha256 `607ebfe3b07f` |
| `GET /api/v1/admin/keys` with the run 1 key | 200, 1 key, `local-admin`, id sha256 `7c6e6bf2151b` | 200, the same 1 key |
| `POST /v1/files` with the run 1 key | 200 | not applicable |
| `GET /v1/files/{id}/content` with the run 1 key | not applicable | 200, `persistent development` |
| `GET /api/v1/models` with the run 1 key | not applicable | 200 |

Starport stores only the SHA-256 hash of a gateway key. Run 2 accepts the run 1
key and lists the same key record, so both runs share one key hash.

Run 1, human lines only (Badger and JSON log lines removed):

```text
Initialized Starport.
Configuration: <scratch>/dx3-accept/home/config/config.env
Data: <scratch>/dx3-accept/home/data
Gateway API key (shown once): <redacted sha256 72b80a9d4e42>
Starport development gateway
URL: http://127.0.0.1:18531
Authentication: required
Console (one-time launch link): http://127.0.0.1:18531/launch?lt=<redacted>

[run_for] exited_early=False exit=0
```

Run 2, human lines only:

```text
Starport development gateway
URL: http://127.0.0.1:18531
Authentication: required
Console (one-time launch link): http://127.0.0.1:18531/launch?lt=<redacted>

[run_for] exited_early=False exit=0
```

`dev --ephemeral --no-open` with `TMPDIR=<scratch>/dx3-accept/tmp` and an empty
home:

| Check | Result |
| --- | --- |
| Exit after SIGINT | 0 |
| Key lines printed | 1, sha256 `145b59bb0981` |
| `TMPDIR` entries while ready | `starport-dev-TLVVM2TI5AGFYXFPU5GFE5VWUX` |
| `TMPDIR` entries after exit | none |
| Home entries after exit | none |

`dev --no-open` with `STARPORT_CONFIG_MANAGEMENT=shared` and an empty home:

```text
load configuration: starport dev requires local management and platform storage: remove STARPORT_CONFIG_MANAGEMENT, run starport serve for this configuration, or run starport dev --ephemeral for a scratch gateway

[run_for] exited_early=True exit=1
```

The home is empty after the refusal: no store, no configuration file, and no
token file.

Correction to the brief: the brief names `STARPORT_MANAGEMENT=shared`. The
setting that selects the management mode is `STARPORT_CONFIG_MANAGEMENT`. The
proof and `TestDevRefusesSharedManagement` use that name.

## Commands and results

| Command | Result |
| --- | --- |
| `go test ./internal/cli/ ./internal/config/ ./cmd/starport/ -count=1` | All 3 packages pass. |
| `go test -timeout 30m ./internal/app/ -count=1` | Pass in 1,031 s. Exit status 0. |
| `go test -timeout 30m -json ./...` | 69 packages pass, 11 packages have no test files, 0 fail. 7,894 tests and subtests pass, 718 skip, 0 fail. `internal/app` takes 1,253 s. Exit status 0. |
| `go test ./cmd/starport/ ./internal/cli/ ./internal/config/ -run 'TestDev\|TestPersistentDevelopment' -count=1 -v` | 4 tests in `cmd/starport` (5 subtests), 9 in `internal/cli`, and 10 in `internal/config` pass. |
| `go test ./internal/cli/ ./internal/config/ -run 'Manifest\|ReferenceFiles' -count=1 -v` | 5 tests in `internal/cli` and 11 in `internal/config` pass. |
| `gofmt -l $(git diff --name-only origin/main -- '*.go')` | No output. |
| `go vet ./...` | Exit status 0, no output. |
| `make docs-generate && git status --short` | `PASS wrote 6 reference files`. No diff. |
| `STARPORT_SMOKE_PORT=18431 scripts/smoke-first-run.sh` | `PASS ephemeral dev, first-run serve, ...`. Exit status 0. |
| `scripts/verify-readme-quickstart.sh` | `PASS README quickstart and dynamic stable-release selection`. |
| `scripts/readme-demo/capture_cli.py --help` | Exit status 0. |
| `python3 scripts/test-native-release.py` | 18 tests pass. |
| `python3 scripts/test-readme-demo.py` | 42 tests pass. |
| `python3 scripts/test-cli-demo.py` | 23 tests pass. |
| `technical-writing lint <path> --mode developer` on each changed page except the operator guide | 0 diagnostics each. |
| `technical-writing lint docs/OPERATOR-GUIDE.md --mode developer` | 68 diagnostics. origin/main also has 68. The changed lines add none. |

## Named tests

| Test | Package | Result |
| --- | --- | --- |
| `TestDevPersistsAcrossRuns` | `cmd/starport` | pass |
| `TestDevRefusesSharedManagement` | `cmd/starport` | pass |
| `TestDevRefusesConfiguredStorage` (5 subtests) | `cmd/starport` | pass |
| `TestDevEphemeralRemovesScratch` | `cmd/starport` | pass |
| `TestDevSecondRunPrintsNoKey` | `internal/cli` | pass |
| `TestDevPrintsGatewayKeyOnce` (persistent and ephemeral cases) | `internal/cli` | pass |
| `TestPersistentDevelopmentChangesOnlyHostLogsAndTLS` | `internal/config` | pass |
| `TestPersistentDevelopmentRefusesBeforeValidation` | `internal/config` | pass |

Persistent dev writes no new session file. It uses the serve files, so the file
manifest needs no change.

## Findings outside this task

- `STARPORT_FILES_PATH` in the process environment has no effect.
  `FilesConfig.Path` in `internal/config/files.go` has the tag `env:"PATH"`
  without `overwrite`. The defaults already set the platform files directory. The Badger and SQLite paths have `overwrite`. A refusal test for
  the files path therefore never refused and served until the test timeout.
  The test uses `STARPORT_FILES_BACKEND=objectstore` and a Badger path in the
  configuration file instead. The defect predates DX3.
- The recorded demo assets under `docs/assets/` keep `starport dev --no-open`.
  They are historical recordings. The `tape_sha256` in
  `docs/assets/cli-current/record.json` no longer matches
  `scripts/cli-demo.tape`. No test checks it. The next recording updates it.
