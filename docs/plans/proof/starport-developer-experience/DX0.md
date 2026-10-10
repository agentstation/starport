# DX0 baseline

Plan: `docs/plans/starport-developer-experience-plan.html`.
Baseline commit: `40d7285f4bcbd51ec87f7cf99a47fa50406aa473` (origin/main, 2026-10-10).
Binary: `go build -o starport .` at the baseline commit, macOS, Go from `go.mod`.
Runner scripts: `tools/run_for.py` runs a command with its output in a file for N seconds, then sends SIGINT. `tools/run_for_pty.py` does the same under a pseudo-terminal.

This file masks every API key, launch token, and scratch path. The raw outputs stay outside the repository.

## Observation 1: port footprint (F4)

```sh
git grep -nP '\b8080\b' -- ':!docs/proof' ':!docs/plans/proof' | wc -l
git grep -lP '\b8080\b' -- ':!docs/proof' ':!docs/plans/proof' | wc -l
git grep -nP 'starport init' -- ':!docs/proof' ':!docs/plans/proof' | wc -l
```

| Pattern | Lines | Files |
| --- | --- | --- |
| `\b8080\b` | 177 | 59 |
| `starport init` | 42 | — |

Top directories for `8080`: `internal/server` 9, `internal/config` 8, `docs/site/start` 4, `internal/identity` 3, `internal/cli` 3, `scripts` 2.

Correction to the plan: `git grep -nE '\b8080\b'` returns 0 lines on the macOS git in use, because its `-E` engine has no `\b`. The plan's DX0 verification command and F4 now say `-P`.

## Observation 2: `serve` on an empty home (F2, F9)

```sh
STARPORT_HOME=<empty-dir> ./starport serve
```

Output:

```text
Welcome to Starport.
  starport ui        Open the console. No key to paste: the link signs
                     this browser in with a session from this machine.
  starport auth url  Print that link instead of opening it.
A gateway API key is for a program calling the gateway, not for the
console. Create one in the console under Keys, or with `starport init`.
create application: provider credential master key is required
```

Exit status: 1.

Files under the home after the failed run:

```text
<home>/data/welcomed
```

`serve` greets before it starts the application (`internal/cli/app.go:179`), so the failed first run leaves the `welcomed` stamp (F9). The error names `starport init` as the only way forward (F2).

## Observation 3: `dev --no-open` twice on one home (F1)

```sh
STARPORT_HOME=<dev-home> tools/run_for.py 8 run1.out ./starport dev --no-open
STARPORT_HOME=<dev-home> tools/run_for.py 8 run2.out ./starport dev --no-open
```

Each run printed the banner, served for 8 seconds, and exited 0 on SIGINT:

```text
Starport development gateway
URL: http://127.0.0.1:8080
Authentication: required
Gateway API key (shown once): STARPO…
Console (one-time launch link): http://127.0.0.1:8080/launch?lt=…
```

Both keys are 88 characters and start with the same scheme prefix. The SHA-256 of each full key differs:

| Run | SHA-256 of the key (first 12 hex) |
| --- | --- |
| 1 | `312ff1265a63` |
| 2 | `53c2b4e0569e` |

The second run printed a new key and a new launch token. `<dev-home>` stays empty after both runs. Each run logged `Local admin token ready: read-only, nothing written to disk` and `development scratch recovery preserved directories without verified ownership` with `preserved=2` for scratch roots under `os.TempDir()` from earlier sessions.

## Observation 4: `dev` with 8080 occupied, output redirected (F5, part 1)

```sh
python3 -m http.server 8080 --bind 127.0.0.1 &
PATH=<fakebin>:$PATH STARPORT_HOME=<busy-home> tools/run_for.py 8 busy.out ./starport dev
```

`<fakebin>/open` is a shell script that appends its arguments to a log and exits 0. It stands in for the macOS `open` that `github.com/pkg/browser` runs.

Output (log lines trimmed):

```text
Starport development gateway
URL: http://127.0.0.1:8080
Authentication: required
Gateway API key (shown once): STARPO…
Console (one-time launch link): http://127.0.0.1:8080/launch?lt=…
Did not open a browser: output is not a terminal.
{"level":"info","port":8080,"host":"127.0.0.1","message":"starting HTTP server"}
{"level":"info","url":"http://127.0.0.1:8080","message":"console ready"}
HTTP server: failed to start server: listen tcp 127.0.0.1:8080: bind: address already in use
```

Exit status: 1. The opener log stays empty. `dev` suppresses the browser when its output is not a terminal (`browserSuppressed`, `internal/cli/app.go:222`). The plan's claim that only `--no-open` gates the browser was wrong. The banner, the `console ready` log, and the bind failure all print before the process exits.

## Observation 5: `dev` with 8080 occupied, under a pseudo-terminal (F5, part 2)

```sh
python3 -m http.server 8080 --bind 127.0.0.1 &
PATH=<fakebin>:$PATH STARPORT_HOME=<busy-home> tools/run_for_pty.py 8 busy-pty.out ./starport dev
```

Opener log after the run:

```text
open http://127.0.0.1:8080/launch?lt=…
```

Gateway output (log lines trimmed):

```text
URL: http://127.0.0.1:8080
Gateway API key (shown once): STARPO…
Console (one-time launch link): http://127.0.0.1:8080/launch?lt=…
{"level":"info","port":8080,"host":"127.0.0.1","message":"starting HTTP server"}
{"level":"info","url":"http://127.0.0.1:8080","message":"console ready"}
HTTP server: failed to start server: listen tcp 127.0.0.1:8080: bind: address already in use
```

Exit status: 1.

The TCP dial in `waitForGateway` (`internal/cli/development.go:119`) succeeded against the foreign listener. `dev` then handed the one-time launch link to the browser opener while its own bind failed. The launch token went to a process that cannot honor it.

## Result

| Finding | Observed |
| --- | --- |
| F1 | Two `dev` runs on one home minted two keys and left the home empty. |
| F2 | `serve` on an empty home exits 1 and names `starport init`. |
| F4 | 177 lines in 59 files carry `8080`. |
| F5 | With 8080 occupied, `dev` opened the launch link against the foreign listener. |
| F9 | The failed `serve` run left `<home>/data/welcomed`. |

No production file changed in DX0.
