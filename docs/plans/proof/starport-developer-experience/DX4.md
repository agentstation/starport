# DX4 bind before open and the developer port fallback

Plan: `docs/plans/starport-developer-experience-plan.html`, task DX4.
Base commit: `9affebbd` (the head of `codex/dx-DX3`, pull request #445).
Branch: `codex/dx-DX4`. Go from `go.mod` (go1.27.2), macOS.

This file shows no API key, master key, or launch token. A secret shows only
as the first 12 hexadecimal characters of its SHA-256 digest. Paths under the
scratch root show as `<scratch>`.

## Fail-before

Commit `a24e8b67` adds `TestServerBindServeClose` and
`TestDevOpensTheBoundAddress` before any behavior change.

```sh
go test ./internal/server/ -run TestServerBindServeClose -count=1 -v
go test ./cmd/starport/ -run TestDevOpensTheBoundAddress -count=1 -v
```

Test lines only (JSON and Badger log lines removed):

```text
# github.com/agentstation/starport/internal/server [github.com/agentstation/starport/internal/server.test]
internal/server/bind_test.go:19:27: gateway.Bind undefined (type *Server has no field or method Bind)
internal/server/bind_test.go:22:25: gateway.Bind undefined (type *Server has no field or method Bind)
internal/server/bind_test.go:27:33: gateway.Serve undefined (type *Server has no field or method Serve)
internal/server/bind_test.go:43:30: gateway.Close undefined (type *Server has no field or method Close)
internal/server/bind_test.go:48:27: gateway.Bind undefined (type *Server has no field or method Bind)
internal/server/bind_test.go:51:30: gateway.Close undefined (type *Server has no field or method Close)
internal/server/bind_test.go:52:30: gateway.Close undefined (type *Server has no field or method Close)
internal/server/bind_test.go:56:28: gateway.Serve undefined (type *Server has no field or method Serve)
FAIL	github.com/agentstation/starport/internal/server [build failed]
=== RUN   TestDevOpensTheBoundAddress
=== RUN   TestDevOpensTheBoundAddress/busy_port
    dev_port_test.go:40: dev printed a link to port 57698, which another process holds: http://127.0.0.1:57698
    dev_port_test.go:40: dev printed a link to port 57698, which another process holds: http://127.0.0.1:57698/launch
=== RUN   TestDevOpensTheBoundAddress/free_port
--- FAIL: TestDevOpensTheBoundAddress (6.26s)
    --- FAIL: TestDevOpensTheBoundAddress/busy_port (3.86s)
    --- PASS: TestDevOpensTheBoundAddress/free_port (2.40s)
FAIL	github.com/agentstation/starport/cmd/starport	7.203s
```

The server test fails at build time because no bind step exists. The dev test
holds the configured port. Before the change, `dev` printed URL and console
links to the port that the other process held. The browser opener polls that
URL, so it opened the console against the other process.

## Change

Commit `84965475` (`server: split bind, serve, and close`):

- The new package `internal/endpoint` owns the listener bind. `Listen` binds
  with a context and returns `*endpoint.BusyError` for `EADDRINUSE` (and
  `WSAEADDRINUSE` on Windows). Its message is `port N is busy`.
- `server.Server` has `Bind`, `Serve`, and `Close`. `Bind` holds the listener
  and returns the bound address. A second `Bind` keeps the first listener.
  `Config.Listener` lets a caller pass a listener that it bound.
- `app.App.Run` binds before it starts the other workers. A bind failure stops
  `Run` before anything serves. The error starts with
  `HTTP server: port N is busy`. A hint follows. It tells the operator to stop
  the other process or to set `STARPORT_SERVER_PORT` to a free port. The
  `serve` command exits 1 with that error and never falls back.

Commit `07c39469` (`cli: fall back to a free developer port`):

- `endpoint.DeveloperPorts` gives the bind order. The configured port,
  port 7373, and port 7678 come first. Next come 20 random ports from 7000
  through 7999, and port 0 is last. The function `endpoint.BindFirst` tries the ports in order. It stops at the first free
  port, and also stops at an error that is not a busy port.
- `config.Config.ServerPortExplicit` reports whether `STARPORT_SERVER_PORT`
  comes from the environment or the configuration file. An explicit port turns
  the fallback off, so `dev` fails like `serve`.
- `dev` (persistent and `--ephemeral`) binds first. Then it sets the
  configured port to the bound port and passes the listener to the
  application. The URL, the console link, the callback base, and the browser
  opener all use the bound port. On a fallback, `dev` prints
  `Port 7827 is busy. Bound port 7373.` before the URL line.

Commit `6b7949b9` (`cli: read the endpoint record in ui and auth url`):

- The gateway writes `gateway-endpoint.json` in `Paths.StateDir` after its
  first bind. The `serve` command and persistent `dev` write it. The
  `dev --ephemeral` command uses a scratch state directory and writes no
  record. The record holds the schema version,
  the scheme, the host, the bound port, the process ID, and the start time. It
  holds no credential. The file mode is `0600`.
- The application lifecycle removes the record before the HTTP server drains.
  The removal keeps a record that another process wrote.
- `starport ui` and `starport auth url` use the record when its process is
  alive. Otherwise they use the configured port, as before. On Unix, the
  liveness check sends signal 0. On Windows, it opens the process handle and
  reads its exit code.
- The file manifest has the role `gateway-endpoint`: owner-only state. A
  backup does not capture it. A restore accepts a backup without it.
- `console/vite.config.ts` reads `STARPORT_CONSOLE_PROXY_TARGET`. The default
  is `http://localhost:7827`. `console/README.md` documents it.
- `docs/ARCHITECTURE.md` lists `internal/endpoint/`.

Documentation in the earlier commits: commit `84965475` adds the `serve`
busy-port error to the operator guide. Commit `07c39469` changes the README,
`docs/site/start/temporary-development.md`, and the operator guide. Each one
now says "7827, or the next free port, which `dev` prints".

## Acceptance with the real binary

The driver `accept.py` in the scratch root runs the binary through
`tools/run_for.py` for 30 s per run. The command output goes through a FIFO.
The driver keeps the key in memory and writes only redacted lines. Each run
uses an `env -i` style environment with `PATH`, `HOME`,
`STARPORT_HOME=<scratch>/home`, `STARPORT_CATALOG_NETWORK_MODE=offline`, and
`STARPORT_CATALOG_ACQUISITION_ENABLED=false`.

```sh
go build -o <scratch>/dx4-bin/starport ./cmd/starport
python3 <scratch>/dx4-accept/accept.py 18471 18472 18473
```

Run 1: `dev --no-open` with `STARPORT_SERVER_PORT=18471`. While it runs, the
driver runs `ui --no-open` and `auth url` with `STARPORT_SERVER_PORT=18472`.
The configured port of the sibling commands is different, so a link on 18471
can come only from the record.

| Check | Result |
| --- | --- |
| Exit after SIGINT | 0 |
| Record during the run | `<scratch>/home/state/gateway-endpoint.json`, mode `0600` |
| Record content | `{"host": "127.0.0.1", "pid": 38063, "port": 18471, "schema_version": 1, "scheme": "http", "started_at": "2026-10-11T16:49:17.146303Z"}` |
| `ui --no-open` during the run | exit 0, `http://127.0.0.1:18471/launch?lt=<redacted>` |
| `auth url` during the run | exit 0, `http://127.0.0.1:18471/launch?lt=<redacted>` |
| Record after exit | absent |
| `ui --no-open` after exit | exit 0, `http://127.0.0.1:18472/launch?lt=<redacted>` (the configured port) |

Run 1, human lines only (Badger and JSON log lines removed):

```text
Initialized Starport.
Configuration: <scratch>/home/config/config.env
Data: <scratch>/home/data
Gateway API key (shown once): <redacted sha256 68783f072236>
Starport development gateway
URL: http://127.0.0.1:18471
Authentication: required
Console (one-time launch link): http://127.0.0.1:18471/launch?lt=<redacted>

[run_for] exited_early=False exit=0
```

Run 2: the driver holds a listener on `127.0.0.1:18473`. Then it runs `serve`
and `dev --no-open` with `STARPORT_SERVER_PORT=18473`.

`serve`, human lines only:

```text
Welcome to Starport.
  starport ui        Open the console. No key to paste: the link signs
                     this browser in with a session from this machine.
  starport auth url  Print that link instead of opening it.
A gateway API key is for a program calling the gateway, not for the
console. Create one in the console under Keys, or with `starport init`.
HTTP server: port 18473 is busy; stop the process that holds it or set STARPORT_SERVER_PORT to a free port
[run_for] exited_early=True exit=1
```

`dev --no-open`:

```text
HTTP server: port 18473 is busy; stop the process that holds it or set STARPORT_SERVER_PORT to a free port
[run_for] exited_early=True exit=1
```

No record exists after the busy runs.

Run 3: the developer port fallback. The fallback applies only when no setting
names the port, so this run uses the standard ports below 18000. The team lead
approved this run. Before the run, no process listened on 7827, 7373, or 7678.
The driver `fallback.py` in the scratch root holds `127.0.0.1:7827` with a
60 s limit. It runs `dev --no-open` through `tools/run_for.py` for 30 s with an
empty `STARPORT_HOME` and no `STARPORT_SERVER_PORT`. The environment is the
same as in run 1.

```sh
python3 <scratch>/dx4-fallback/fallback.py
```

| Check | Result |
| --- | --- |
| Exit after SIGINT | 0 |
| Listeners while the run is ready | 7827 (the hold) and 7373 (`dev`) |
| Record during the run | mode `0600`, `{"host": "127.0.0.1", "pid": 61784, "port": 7373, "schema_version": 1, "scheme": "http", "started_at": "2026-10-11T17:07:29.226571Z"}` |
| `ui --no-open` during the run, no port setting | exit 0, `http://127.0.0.1:7373/launch?lt=<redacted>` |
| Record after exit | absent |
| Hold time | 30.1 s |
| Listeners on 7827, 7373, and 7678 after the run | none |

Run 3, human lines only (Badger and JSON log lines removed):

```text
Initialized Starport.
Configuration: <scratch>/home/config/config.env
Data: <scratch>/home/data
Gateway API key (shown once): <redacted sha256 e9fbaf92f19f>
Starport development gateway
Port 7827 is busy. Bound port 7373.
URL: http://127.0.0.1:7373
Authentication: required
Console (one-time launch link): http://127.0.0.1:7373/launch?lt=<redacted>
[run_for] exited_early=False exit=0
```

## Commands and results

| Command | Result |
| --- | --- |
| `go test -timeout 30m ./internal/endpoint/ ./internal/server/ ./internal/app/ ./internal/cli/ ./cmd/starport/ ./internal/config/` | All 6 packages pass. `internal/app` takes 706 s. |
| `go test -race -count=1 ./internal/server/ ./internal/cli/ ./internal/endpoint/` | All 3 packages pass. |
| `go test -race -count=1 ./cmd/starport/` | Pass in 212 s. |
| `go test -timeout 40m -json ./...` | 70 packages pass, 11 packages have no test files, 0 fail. 7,929 tests and subtests pass, 718 skip, 0 fail. `internal/app` takes 1,389 s. Exit status 0. |
| `gofmt -l $(git diff --name-only origin/main -- '*.go')` | No output. |
| `go vet ./...` | Exit status 0, no output. |
| `GOOS=windows go vet ./internal/endpoint/ ./internal/server/ ./internal/cli/ ./cmd/starport/` | Exit status 0. |
| `GOOS=windows go build ./...` and `GOOS=linux go build ./...` | Exit status 0. |
| `make lint` | `0 issues.` |
| `make docs-generate && git status --short` | `PASS wrote 6 reference files`. No diff. |
| `STARPORT_SMOKE_PORT=18431 scripts/smoke-first-run.sh` | `PASS ephemeral dev, first-run serve, ...`. Exit status 0. |
| `scripts/verify-readme-quickstart.sh` | `PASS README quickstart and dynamic stable-release selection`. |
| `python3 scripts/test-native-release.py` | 18 tests pass. |
| `python3 scripts/test-readme-demo.py` | 42 tests pass. |
| `python3 scripts/test-cli-demo.py` | 23 tests pass. |
| `pnpm install --frozen-lockfile` in `console/` (ignored `node_modules`) | Exit status 0. |
| `pnpm lint`, `pnpm build`, `pnpm typecheck` in `console/` | Exit status 0 each. |
| `pnpm test` in `console/` | 83 files, 539 tests pass. |
| `go build ./...` in a `git checkout-index` copy of each commit | Exit status 0 for each commit. |
| `technical-writing lint <path> --mode developer` on each changed page | No new diagnostics. See the next section. |

`pnpm typecheck` needs the route tree that `pnpm build` generates. Run it after
the build, as `pnpm check` does.

## Named tests

| Test | Package | Result |
| --- | --- | --- |
| `TestServerBindServeClose` (4 subtests) | `internal/server` | pass |
| `TestDevOpensTheBoundAddress` (busy, free, and fallback subtests) | `cmd/starport` | pass |
| `TestDevFallsBackInOrder` (4 subtests) | `internal/endpoint` | pass |
| `TestDevFallsBackToAnyPortAfterTwentyRandomFailures` | `internal/endpoint` | pass |
| `TestDevReportsTheBusyPort` | `internal/cli` | pass |
| `TestDevKeepsExplicitPort` | `cmd/starport` | pass |
| `TestDevKeepsConfigurationFilePort` | `cmd/starport` | pass |
| `TestDevWritesEndpointRecord` | `cmd/starport` | pass |
| `TestUIReadsEndpointRecord` | `internal/cli` | pass |
| `TestServeFailsWhenPortIsBusy` | `cmd/starport` | pass |
| `TestServerPortExplicit` (3 subtests) | `internal/config` | pass |
| `TestBindFirstStopsOnAnotherError`, `TestBindFirstNamesTheFirstBusyPort` | `internal/endpoint` | pass |
| `TestRecordRoundTripsForALiveProcess`, `TestReadRecordIgnoresAMissingOrExitedRecord`, `TestReadRecordRefusesAnUnknownSchema`, `TestRemoveRecordKeepsAnotherProcessRecord` | `internal/endpoint` | pass |
| `TestRestoreInventoryAcceptsABackupWithoutTheEndpointRole` | `internal/config` | pass |

The file manifest tests include the record. The test in `internal/cli`
checks its path, availability, and owner-only access. The tests in
`internal/config` check three more points. Ephemeral `dev` disables the
record. A backup does not capture it. A restore accepts a backup without it.

Two mutation checks confirm that the tests catch a regression. With the
explicit-port flag always false, `TestDevKeepsExplicitPort` and
`TestDevKeepsConfigurationFilePort` fail. Without the restore exemption,
`TestRestoreInventoryAcceptsABackupWithoutTheEndpointRole` fails with
`omits canonical role gateway-endpoint`.

## Markdown lint

| Page | Diagnostics (base, branch) |
| --- | --- |
| `README.md` | 0, 0 |
| `console/README.md` | 0, 0 |
| `docs/ARCHITECTURE.md` | 26, 26 |
| `docs/OPERATOR-GUIDE.md` | 68, 68 |
| `docs/site/start/temporary-development.md` | 0, 0 |
| `docs/plans/proof/starport-developer-experience/DX4.md` | not applicable, 0 |

## Limits and open points

- Windows: `Listen` maps `WSAEADDRINUSE` to the busy-port error. It does not
  map `WSAEACCES`, which Windows returns for a port in an excluded range. Such
  a port ends the fallback search with the bind error. The Windows liveness
  check compiles but did not run on Windows.
- A reused process ID can make a stale record look alive after a crash. Then
  `ui` prints a link to the recorded port. The next gateway start replaces the
  record.
- `ui` and `auth url` ignore a record that does not parse or that has an
  unknown schema. They use the configured port and show no warning.
- On macOS, the busy-port check for the wildcard host depends on the socket
  options of the holder. `serve` uses the default host `0.0.0.0`. In the
  acceptance run, the holder was a Python socket on `127.0.0.1` without
  `SO_REUSEADDR`, and `serve` reported the busy port.
- A separate Go check held `127.0.0.1:18476` with `net.Listen`. Then
  `net.Listen` on `0.0.0.0:18476` and on `:18476` both succeeded, because Go
  sets `SO_REUSEADDR`. Thus `serve` on the wildcard host can start beside a Go
  process, such as `starport dev`, that holds the loopback port. Local
  requests then reach the more specific listener. `dev` binds `127.0.0.1`, so
  the `dev` fallback is not affected. The behavior predates DX4.
- A new backup lists the `gateway-endpoint` role with the capture
  `not-selected`. The restore in an older binary rejects a role that it does
  not know, so it refuses such a backup.
- Persistent `dev` delivers the first-run key before the bind. When an
  explicit port is busy on the first run, `dev` prints the key and then fails.
  The next run uses the same key.
