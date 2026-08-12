# POR7 Starport connector-owned HTTP transport proof

POR7 starts from clean Starport `main` at
`03651443b8d71884615a635c6f4b4a52acad57aa`.

The task will place required provider HTTP transport policy with the connector
concept that owns it. It will remove unused generic production surfaces and
duplicate elapsed-time ownership without changing connector behavior.

## Fail-before evidence

The focused baseline passes all five packages:

```text
go test -count=1 ./internal/httpclient ./internal/providers/connectors ./internal/execution ./internal/router ./internal/architecture
```

The generic package contains eight Go files and 14 exported declarations. Its
only production caller uses `DefaultConfig`, `New`, and `GetHTTPClient` in the
private connector builder. The remaining surface is package-owned code and
self-tests.

The production client has a five-minute `http.Client.Timeout`. This total
timeout duplicates the execution-owned elapsed budget and can stop a healthy
stream body. Its monitored transport measures elapsed time again and adds
`X-HTTP-Client-Provider` and `X-HTTP-Client-Duration-Ms` to provider response
headers.

The unused rate-limit wrapper is the only Starport source that imports
`golang.org/x/time/rate`. The baseline lists `golang.org/x/time v0.15.0` as a
direct requirement for that source. None of the four required POR7 contract
tests exists at the baseline.

After source removal, `go mod tidy` keeps `golang.org/x/time v0.15.0` as an
indirect requirement. `go mod why -m` proves that the Vault client imports
`golang.org/x/time/rate`. The module graph also names OpenBao, Google auth,
Starmap, and Google API modules as consumers. POR7 therefore rejects a direct
Starport requirement or connector import. It preserves the valid transitive
module requirement.

## Focused implementation evidence

The private connector builder now owns pool limits, dialing, TLS handshakes,
idle connections, redirects, HTTP/2, and the response-header timeout. It does
not set `http.Client.Timeout`. It does not measure elapsed request time or
change provider response headers. Execution contexts remain the one owner of
the total retry and fallback deadline.

The old package and its eight files are gone. The direct `golang.org/x/time`
requirement is gone. `go mod tidy` retains the verified indirect requirement.
Package-layout guards reject both the old import path and package declaration.

The four named acceptance contracts pass. A behavior test also proves that an
OpenAI-compatible production connector preserves its caller's exact deadline.
The focused ordinary and uncapped race tests pass for connectors, execution,
router, and architecture. Package-layout checks and regression tests pass.
Architecture and Starmap ownership each report 12 passed and 0 failed.
Documentation links and strict technical-writing checks pass.

The campaign verifier passes POR-V01 through POR-V08 and reports 8 passed and
1 failed. POR-V09 remains red for cross-repository closeout.

## Full local verification

The complete repository checks pass with normal, uncapped Go scheduling:

```text
make verify
go test ./...
go vet ./...
make lint
make build
bash scripts/smoke-openrouter-sdks.sh
```

`make verify` reports Starmap ownership 12/12, architecture 12/12, release
15/15, release workflow success, developer experience 47/47, documentation
link success, and documentation-link regression success. All 41 Go packages
pass. Vet reports no diagnostics. Lint reports zero issues. The build completes.
All seven SDK smoke cases pass.

## Commit and review

Starport commit `15d0acc` contains the verified POR7 diff. The automatic review
selected Claude Opus 5, but its connection stopped on a local self-signed
certificate before it read any tokens. The fallback GPT-5.6-sol review ran in
isolation and found no actionable defect. TruffleHog reported a clean
changed-content scan.

Starport PR [#109](https://github.com/agentstation/starport/pull/109)
publishes exact reviewed head `15d0acc` for the hosted gates.

## Hosted and merge evidence

All 10 hosted checks passed on exact head
`15d0acc6133119b5d7e32ada26724187ac569e7d`. They covered lint and Linux,
macOS, and Windows tests. They also covered security, release contracts, the
release snapshot, SDK compatibility, action provenance, and the build.

PR #109 squash-merged as
`924855c9d6cda6c12060cad91cd99bf3d21094a8`. The local Starport `main`
branch is clean and matches `origin/main` at that commit. The campaign verifier
on protected `main` passes POR-V01 through POR-V08 and reports 8 passed and 1
failed. POR-V09 remains red for cross-repository closeout.
