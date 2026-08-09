# DX2 configuration proof

Date: 2026-08-09

## Fail-before state

Focused tests produced these failures before the implementation:

```text
expected default host 127.0.0.1, got 0.0.0.0
expected CORS to be disabled by default
configuration loading changed the process environment
SecurityConfig.Validate() error = <nil>, wantErr true
```

The source also used launch-directory paths for Badger data and rate-limit
rules.

## Implementation

Work commit: `0fd526598c85fc4cb2aa49a21ca47891eaf7ef89`

- `Paths` derives one configuration file and managed data paths from
  `os.UserConfigDir`.
- `Loader` reads environment files through lookup sources. It does not add
  their values to the process environment.
- Process environment values override files. The first file value overrides
  later file values.
- Relative configured paths use the platform configuration directory.
- Local HTTP uses `127.0.0.1`. CORS and rate-limit reload start off.
- A supplied credential master key must contain at least 32 bytes.
- The container image sets a writable XDG root, public container binding, and
  a persistent Badger path.

## Focused verification

Commands:

```bash
go test ./internal/config ./internal/app -count=1
go test -race ./internal/config ./internal/app -count=1
bash scripts/verify-developer-experience.sh
```

Results:

```text
ok github.com/agentstation/starport/internal/config
ok github.com/agentstation/starport/internal/app
Summary: 11 passed, 28 failed
```

The 28 verifier failures belong to later plan tasks. All seven DX2 conditions
pass.

## Repository gates

These commands passed:

```bash
bash scripts/verify-starmap-ownership.sh
bash scripts/verify-v1-architecture.sh
go test ./...
go vet ./...
make lint
make build
bash scripts/smoke-openrouter-sdks.sh
```

The ownership verifier passed 11 checks. The architecture verifier passed 12
checks. The SDK smoke suite passed raw HTTP and the Python, TypeScript, and Go
OpenRouter clients.

Strict technical-writing lint passed the configuration guide and extracted
configuration-reference comments. The glossary check reported 16 terms and
zero errors.

## Container verification

Commands:

```bash
docker build -t starport:dx2 .
docker run --rm starport:dx2 version
```

The image built for Linux ARM64. The non-root container ran the Starport binary
and reported Go 1.26.5.

## Autoreview

The automatic profile selected Claude Opus 5 at high reasoning. Its transport
stopped before review because the local proxy supplied a self-signed
certificate. It produced no finding.

The isolated `sol` profile then used `gpt-5.6-sol` at high reasoning. TruffleHog
reported a clean bundle. The reviewer reported no actionable finding and rated
the patch correct at 0.91. It supplied no finding to accept or reject.

The final branch-bundle review also used the isolated `sol` profile. It rated
the patch correct at 0.97 and reported no accepted finding. It noted that the
new sources and paths do not support an in-place upgrade from the old defaults.
The owner requires direct pre-launch changes and forbids legacy compatibility,
so the task rejects that concern by design.

## Pull request gate

- Pull request: `https://github.com/agentstation/starport/pull/78`
- Merge commit: `be127cef32e4c69a474bea2289900d3d2009abda`
- Merge time: 2026-08-09 at 21:18:57 UTC

All 10 CI checks passed before merge:

- Action Pin Provenance
- Build
- Lint
- OpenRouter SDK Compatibility
- Release Contract
- Release Snapshot
- Security Scan
- Test on macOS
- Test on Ubuntu
- Test on Windows
