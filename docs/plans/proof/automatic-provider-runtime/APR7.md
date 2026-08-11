# APR7 integration, release, and readback proof

Status: done  
Work commits: `c35177c`, `e1451c9`  
Release commit: `a5d9050e2fdedb274e7d7aaa6b41ee797fce725f`

## Result

APR7 published immutable Starport `v1.0.3` from the exact public `main` head.
The release contains 13 assets. The set includes six platform archives, six
Syft SBOMs, and `checksums.txt`.

The release workflow also published the multi-platform GHCR image at this
digest:

```text
sha256:a90373ce690fb5ef836a4b4af3d6d82a931db7eca7865031eb9b6ffbb1dfea6a
```

The Homebrew publisher wrote cask version `1.0.3` to the tap `main` branch in
commit `d0666a6b835ada84d82c744c4759220c9a0a1e42`. Exact-version installation
passed on macOS and Linux.

The independent public verifier checks the release outside the tag workflow.
It verifies the tag, workflow, assets, release attestation, build provenance,
packaged README, native binary, ephemeral development gateway, cask, container,
and container provenance.

## Fail-before evidence

Before publication, the default `v1.0.3` readback failed with GitHub HTTP 404.
The existing `v1.0.2` binary also failed the new binary contract because it did
not contain the provider-neutral `dev` command.

The first post-release readback found a verifier defect. The `grep -q` command
closed its pipe after the first match. The released help process then exited
with SIGPIPE status `141` under `pipefail`. Direct inspection proved that the
released binary contained `dev` and that `starport dev --help` exited with
status `0`. Commit `e1451c9` changed the verifier to call that command directly.

## Dependency evidence

The release audit found these current immutable tool releases on 2026-08-11:

- GoReleaser `v2.17.1` is current.
- Syft `v1.51.0` is current.

Syft `v1.51.0` replaced `v1.50.0` on 2026-08-10. Its release notes record two
remediated `go-git` vulnerabilities. CI, the tag workflow, and the local
snapshot now use the same reviewed Syft version.

## Pull request evidence

PR [#102](https://github.com/agentstation/starport/pull/102) reviewed exact head
`c35177c8e539a5e89f84f24328ceeac3624e7e39`. It merged as
`a5d9050e2fdedb274e7d7aaa6b41ee797fce725f` after these 10 checks passed:

```text
Action Pin Provenance
Build
Lint
OpenRouter SDK Compatibility
Release Contract
Release Snapshot
Security Scan
Test (macos-latest)
Test (ubuntu-latest)
Test (windows-latest)
```

The default autoreview selected Claude Opus 5 high. The local proxy certificate
stopped that harness before token use. TruffleHog passed. The Sol profile then
reviewed the exact branch diff with GPT-5.6-sol high. It reported no actionable
findings and rated the patch correct with confidence `0.99`.

## Release workflow evidence

Release run
[31540601957](https://github.com/agentstation/starport/actions/runs/31540601957)
used push event `v1.0.3` at exact commit
`a5d9050e2fdedb274e7d7aaa6b41ee797fce725f`. These jobs passed:

```text
Release Gate                              4m49s
Assemble, Verify, and Publish            10m01s
Verify Homebrew (macos-latest)              24s
Verify Homebrew (ubuntu-latest)             39s
```

The workflow published the release only after it verified every draft asset
and its provenance. The public release reports `draft=false`,
`prerelease=false`, and `immutable=true`.

Homebrew emitted tap-trust information during setup. This did not weaken the
test. Both jobs installed the fully qualified cask and verified version
`1.0.3`. Homebrew documents that a fully qualified install trusts only that
item, not the complete third-party tap.

## Public readback evidence

The corrected public verifier returned:

```text
PASS immutable v1.0.3 automatic-provider release at a5d9050e2fdedb274e7d7aaa6b41ee797fce725f with 13 assets, cask, and container sha256:a90373ce690fb5ef836a4b4af3d6d82a931db7eca7865031eb9b6ffbb1dfea6a
```

The verifier also started the released native binary through `starport dev`.
It authenticated `GET /api/v1/admin/providers` with the one-time gateway API
key and confirmed that the process wrote no persistent configuration.

## Verification evidence

These local checks passed with normal Go scheduling:

```text
bash scripts/verify-starmap-ownership.sh     12 passed, 0 failed
bash scripts/verify-v1-architecture.sh       12 passed, 0 failed
bash scripts/verify-v1-release.sh            15 passed, 0 failed
bash scripts/verify-release-workflow.sh      pass
bash scripts/verify-developer-experience.sh  46 passed, 0 failed
bash scripts/verify-automatic-provider-runtime.sh  12 passed, 0 failed
bash scripts/verify-doc-links.sh             pass
bash scripts/test-doc-link-verifier.sh       pass
bash scripts/smoke-first-run.sh              pass
go test ./...                                pass
go test -race ./...                          pass
go vet ./...                                 pass
make lint                                    0 issues
make build                                   pass
bash scripts/smoke-openrouter-sdks.sh        4 raw and 3 SDK checks
make release-snapshot                        6 binaries, 6 archives, 6 SBOMs
shellcheck public release verifier           pass
git diff --check                             pass
```

No command used `GOFLAGS=-p` or another scheduler cap.
