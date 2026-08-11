# APR6 developer documentation proof

Status: done  
Work commit: `6e98bc8`

## Result

APR6 replaces the stateful first-run path with the tested development path.
The README now starts with two explicit terminal roles:

- Terminal 1 sets any wanted provider credential and runs `starport dev`.
- Terminal 2 owns the printed Starport gateway key and sends client requests.

The quickstart uses plain commands. It contains no `STARPORT_BIN` indirection
and no provider-specific initialization. It explains that `starport dev` binds
to loopback, uses in-memory storage, creates no configuration file, and prints
one temporary gateway key.

The provider contract now matches the implemented architecture. Starmap owns
provider IDs, exact credential fields, conventional environment names, ordered
inference profiles, endpoints, and authentication primitives. Starport
registers each catalog provider when its compiled registries support the
required transport and authentication primitives. It separately resolves
deployment-owned material from the ordered Starmap profiles. Missing operator
material does not block gateway readiness or tenant BYOK.

The operator guide documents startup, one-minute interval reconciliation, and
authenticated manual refresh. It also documents these boundaries:

- Process environment changes require a restart.
- Renewable cloud and direct secret sources can change material through their
  lifecycle.
- Discovery sends no billable inference request.
- A resolved credential is not proof that the provider accepts it.
- Provider state keeps adapter, operator credential, and offering state
  separate and secret-free.

The architecture guide no longer says that ambient cloud credentials cannot
activate an adapter. It now describes catalog-declared cloud profiles, the
provider reconciler, atomic runtime publication, and the provider-state
projection.

The container path also has no provider roster. Compose reads an optional,
operator-owned `.env` file and keeps only Starport infrastructure values in its
explicit `environment` map. The current Docker Compose service contract
supports `env_file.required: false` as of Compose 2.24.0. See the
[Docker service reference](https://docs.docker.com/reference/compose-file/services/#env_file).

The README resolves the latest stable release tag once through GitHub CLI and
uses that exact version for pull, attestation, and execution. It does not
contain a numeric container tag that becomes stale after publication.

## Fail-before evidence

Before APR6, the README contained all four rejected patterns:

```text
export STARPORT_BIN="${STARPORT_BIN:-starport}"
"$STARPORT_BIN" init
"$STARPORT_BIN" serve
ghcr.io/agentstation/starport:1.0.0
```

The gateway-key environment value appeared before the instruction to start the
server, which made server and client terminal ownership unclear. The Compose
service also required `OPENAI_API_KEY` directly.

The new verifier regression suite constructs one fixture for each failure. It
rejects:

- `STARPORT_BIN`.
- `starport init --provider`.
- A quickstart in which Terminal 1 does not own `starport dev`.
- A numeric GHCR tag that can become stale.

## Acceptance evidence

The DX and campaign verifiers returned the expected APR6 state:

```text
PASS README quickstart and dynamic stable-release selection
PASS README quickstart verifier regression tests
Summary: 46 passed, 0 failed

PASS APR-V11 the tested quickstart uses current plain commands
FAIL APR-V12 repository and public release readback gates pass
Summary: 11 passed, 1 failed
```

APR-V12 remains assigned to APR7. The campaign verifier exited with status `1`
because APR7 has not published the new runtime release.

The first-run smoke started `starport dev` and authenticated with its one-time
key. It proved that the command created no persistent configuration. It then
verified isolated initialization, diagnosis, readiness, and authenticated model
discovery.

The exact public container commands selected `v1.0.2`, verified its GitHub
attestation, and returned `starport version 1.0.2`. Compose also parsed in a
temporary directory with no provider-specific environment field.

## Verification evidence

The following checks passed with normal Go scheduling:

```text
technical-writing lint                              4 files, 0 diagnostics
bash scripts/verify-doc-links.sh                    pass
bash scripts/test-doc-link-verifier.sh              pass
bash scripts/verify-developer-experience.sh         46 passed, 0 failed
bash scripts/smoke-first-run.sh                     pass
bash scripts/verify-starmap-ownership.sh            12 passed, 0 failed
bash scripts/verify-v1-architecture.sh              12 passed, 0 failed
go test ./...                                       pass
go test -race ./...                                 pass
go vet ./...                                        pass
make lint                                           0 issues
make build                                          pass
bash scripts/smoke-openrouter-sdks.sh               4 raw and 3 SDK checks
git diff --check                                    pass
```

No command used `GOFLAGS=-p` or another scheduler cap. The slowest race package
was `internal/app` at 134.138 seconds. It completed normally.

The default autoreview selected Claude Opus 5 high. Its harness stopped before
token use because the local proxy supplied a self-signed certificate. The
secret scan passed. The Sol profile reviewed the exact `6e98bc8` work commit
with GPT-5.6-sol high. It reported no accepted or actionable findings and rated
the patch correct with confidence `0.99`.
