# APR1 provider-neutral bootstrap proof

Status: done  
Work commit: `0a31858`

## Result

APR1 separates gateway bootstrap from provider credential discovery.

- `starport init` creates local security and identity state.
- The command never selects, reads, writes, or prints provider credential material.
- The removed `--provider` flag returns a usage error.
- `starport dev` binds to `127.0.0.1` and uses Badger in-memory mode.
- Development mode reads process settings without reading `config.env`.
- Development mode creates an ephemeral master key and gateway API key.
- Production bootstrap remains explicit and fails closed.

The implementation added no module dependency and no compatibility path.

## Fail-before evidence

APR0 recorded both missing acceptance tests and the prior behavior. Local setup
required one provider and copied ambient provider material into `config.env`.
The application also rejected an empty provider set.

The APR0 verifier result was `1 passed, 11 failed`.

## Acceptance evidence

The campaign verifier returned the expected APR1 state:

```text
PASS APR-V01 provider-neutral bootstrap persists no provider credential
PASS APR-V02 local development uses loopback and in-memory storage
PASS APR-V05 Starmap inference profiles and order drive resolution
Summary: 3 passed, 9 failed
```

APR-V03 through APR-V04 and APR-V06 through APR-V12 remain assigned to later
tasks. The verifier exited with status `1` because those planned assertions are
still red.

These named tests passed:

```text
TestInitRejectsProviderFlag
TestLocalInitPersistsNoProviderCredential
TestDevUsesInMemoryBadger
TestDevBindsLoopbackOnly
TestDevPrintsGatewayKeyOnce
```

The first-run smoke passed. It started `starport dev` without a configuration
file or provider material. It also verified authenticated access and no
persistent configuration directory. The production path then passed plain
initialization, diagnosis, readiness, and authenticated model discovery.

## Verification evidence

The following checks passed with normal Go scheduling:

```text
go test ./...                                      41 packages, 35 with tests
go vet ./...                                       pass
make lint                                          0 issues
make build                                         pass
go test -race ./internal/cli ./internal/setup \
  ./internal/storage ./internal/config \
  ./internal/app ./cmd/starport                    6 packages
bash scripts/verify-starmap-ownership.sh           12 passed, 0 failed
bash scripts/verify-v1-architecture.sh             12 passed, 0 failed
bash scripts/verify-developer-experience.sh        41 passed, 0 failed
bash scripts/verify-doc-links.sh                    pass
bash scripts/smoke-first-run.sh                    pass
bash scripts/smoke-openrouter-sdks.sh              4 raw and 3 SDK checks
technical-writing lint on changed docs             3 files, 0 diagnostics
technical-writing lint on the active plan          1 file, 0 diagnostics
git diff --check                                   pass
```

The SDK smoke covered the official Python, TypeScript, and Go clients.

The default autoreview selected Claude Opus 5 high. Its harness stopped before
token use because the local proxy supplied a self-signed certificate. The
secret scan passed. The Sol profile then reviewed the same branch diff with
GPT-5.6-sol high and reported no accepted or actionable findings. Its overall
correctness confidence was `0.97`.

No check used `GOFLAGS=-p` or another scheduler cap.
