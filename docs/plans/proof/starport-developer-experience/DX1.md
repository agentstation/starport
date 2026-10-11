# DX1 standard port

Plan: `docs/plans/starport-developer-experience-plan.html`.
Base commit: `c027e2b3` (origin/main, 2026-10-10).
Branch: `codex/dx-DX1`.

## Change

`internal/config.DefaultPort` is `7827`. It owns the standard port. The tags on `config.ServerConfig.Port` and `server.Config.Port` repeat the number, because a struct tag holds a literal. Two tests prove that the tags and the constant agree:

- `TestPortDefaultMatchesItsConstant` in `internal/config/loader_test.go`.
- `TestConfigPortDefaultMatchesTheStandardPort` in `internal/server/config_test.go`.

`TestLoaderSecurePlatformDefaults` pins the value `7827`. `STARPORT_SERVER_PORT` still overrides the default.

The Dockerfile exposes `7827`. `docker-compose.yml` maps `127.0.0.1:${STARPORT_PORT:-7827}` to container port `7827`. `docker-compose.fleet.yml` maps a free loopback host port to container port `7827`. The Vite proxy, `.env.example`, the `Makefile` message, the example, and the demo and storage scripts move to `7827`. The Go test fixtures, the documents, the agent skill, and the splash facts move with them. `make docs-generate` refreshed `docs/site/generated/settings.json` and `docs/site/generated/settings.md`.

The README and the operator guide carry the compatibility note. The default changed from 8080 to 7827. `STARPORT_SERVER_PORT=8080` restores the old port. With Compose, `STARPORT_PORT=8080` restores the old host port.

`scripts/smoke-first-run.sh` keeps its isolated port `18080`. It sets `STARPORT_SERVER_PORT` from `STARPORT_SMOKE_PORT` and never reads the default, so the `\b8080\b` pattern does not match it.

## Remaining 8080 lines

```sh
git grep -nP '\b8080\b' -- ':!docs/proof' ':!docs/plans/proof' ':!docs/plans'
```

9 lines in 4 files, down from 177 lines in 59 files on the DX0 baseline.

| Line | Reason |
| --- | --- |
| `README.md:386` | Compatibility note. It names the old port. |
| `README.md:387` | Compatibility note. It names `STARPORT_SERVER_PORT=8080`. |
| `README.md:388` | Compatibility note. It names `STARPORT_PORT=8080` for Compose. |
| `docs/OPERATOR-GUIDE.md:351` | Compatibility note. It names the old port and `STARPORT_SERVER_PORT=8080`. |
| `docs/OPERATOR-GUIDE.md:353` | Compatibility note. It names `STARPORT_PORT=8080` for Compose. |
| `docs/OPERATOR-GUIDE.md:355` | Compatibility note. It tells the operator to change clients that name port 8080. |
| `docs/assets/first-use-v1.2.0/TRANSCRIPT.md:25` | Recorded v1.2.0 transcript. It stays unchanged. |
| `docs/site/operate-starmap/central-server.md:12` | Starmap server default. `starmap serve` defaults to port 8080 (`starmap/internal/cli/commands/serve/command.go:70`). Starport does not own that value. |
| `docs/site/operate-starmap/central-server.md:78` | Starmap server step. It passes `--port 8080` to `starmap serve`, the Starmap default. |

The plan file also matches. Its lines record the baseline finding, the port review, and the ledger. The orchestrator owns that file.

## Verification

### Go tests

```sh
go test ./internal/config/ ./internal/server/ ./internal/cli/
```

```text
ok  	github.com/agentstation/starport/internal/config	24.604s
ok  	github.com/agentstation/starport/internal/server	12.991s
ok  	github.com/agentstation/starport/internal/cli	2.822s
```

A `go test -count=1 -json` run of the same packages counted 1,236 passed tests, 7 skipped tests, and 0 failed tests.

```sh
go test -count=1 ./internal/config/ -run TestReferenceFilesMatchCommittedFiles -v
```

```text
--- PASS: TestReferenceFilesMatchCommittedFiles (0.01s)
ok  	github.com/agentstation/starport/internal/config	0.899s
```

The other packages with changed test fixtures also pass:

```text
ok  	github.com/agentstation/starport/cmd/starport	3.114s
ok  	github.com/agentstation/starport/internal/app	581.890s
ok  	github.com/agentstation/starport/internal/authmode	0.623s
ok  	github.com/agentstation/starport/internal/catalog	204.330s
ok  	github.com/agentstation/starport/internal/identity	1.030s
ok  	github.com/agentstation/starport/internal/localauth	2.138s
ok  	github.com/agentstation/starport/internal/providers/connectors	5.733s
```

### Generated references

```sh
make docs-generate
```

```text
PASS wrote 6 reference files to docs/site/generated
```

The generator wrote the same `settings.json` and `settings.md` content as the edit. No other generated file changed.

### Compose

```sh
docker compose config --quiet
```

```text
required variable STARPORT_SECURITY_MASTER_KEY is missing a value: set STARPORT_SECURITY_MASTER_KEY
```

Exit status: 1. The base commit gives the same result, because the file requires the master key. With a placeholder key, the command passes and shows the new mapping:

```sh
STARPORT_SECURITY_MASTER_KEY=x docker compose config --quiet   # exit 0
STARPORT_SECURITY_MASTER_KEY=x docker compose config            # ports excerpt
```

```text
    ports:
      - mode: ingress
        host_ip: 127.0.0.1
        target: 7827
        published: "7827"
```

The fleet file requires `.env.fleet`. A scratch copy of `docker-compose.fleet.yml` with `.env.fleet.example` as `.env.fleet` and placeholder values passes `docker compose -f docker-compose.fleet.yml config --quiet` (exit 0) and shows `target: 7827`.

### Console build

```sh
pnpm --dir console install --frozen-lockfile
npm --prefix console run build
```

Exit status: 0. The build ends with `built in 1.46s`.

### Format and vet

```sh
gofmt -l $(git diff --name-only origin/main -- '*.go') internal/server/config_test.go
go vet ./...
```

`gofmt` prints nothing. `go vet` exits 0.

### Repository checks

| Command | Result |
| --- | --- |
| `make lint` | `0 issues.` |
| `bash scripts/verify-readme-quickstart.sh` | `PASS README quickstart and dynamic stable-release selection` |
| `bash scripts/verify-developer-experience.sh` | `Summary: 47 passed, 0 failed` |
| `bash scripts/verify-doc-links.sh` | `PASS documentation links` |
| `bash scripts/test-doc-link-verifier.sh` | `PASS documentation link verifier edge cases` |
| `bash scripts/verify-v1-release.sh` | `Summary: 16 passed, 0 failed` |
| `bash scripts/verify-package-layout.sh` | `package-layout verification passed` |
| `bash scripts/smoke-first-run.sh` | `PASS ephemeral dev, isolated init, validation, diagnosis, readiness, and authenticated model discovery` |
| `python3 scripts/test-cli-demo.py` | `Ran 23 tests`, `OK` |

### Runtime

A binary from this branch ran `starport dev --no-open` on an empty scratch home with no port override:

```text
health/live on 7827: 200
URL: http://127.0.0.1:7827
Console (one-time launch link): http://127.0.0.1:7827/launch?lt=…
{"level":"info","scheme":"http","port":7827,"host":"127.0.0.1",…,"message":"starting HTTP server"}
```

The same binary with `STARPORT_SERVER_PORT=8080`:

```text
health/live on 8080 with STARPORT_SERVER_PORT=8080: 200
URL: http://127.0.0.1:8080
```

Both runs exited 0 on SIGINT.

### Prose lint

```sh
"$TECHNICAL_WRITING" lint <path> --mode strict --format text
```

| Document | Base | After |
| --- | --- | --- |
| `README.md` | 0 | 0 |
| `console/README.md` | 3 | 0 |
| `internal/config/README.md` | 1 | 0 |
| `docs/OPERATOR-GUIDE.md` | 68 | 68 |
| `skills/starport/SKILL.md` | 0 | 0 |
| `docs/site/generated/settings.md` | 0 | 0 |
| 11 other changed pages under `docs/site/` | 0 each | 0 each |

The 68 operator guide diagnostics are in sections that DX1 does not change. The compatibility note at lines 351 through 355 adds none.
