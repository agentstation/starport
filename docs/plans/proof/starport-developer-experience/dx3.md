# DX3 CLI foundation proof

Date: 2026-08-09

## Fail-before state

- The process imported `urfave/cli/v2`.
- Running the binary without a command started the server.
- Command tests replaced `os.Args` and `os.Stdout`.
- The process used `log.Fatalf` for returned errors.
- The container image had no default `serve` argument.
- The version command wrote through global process output and had no JSON form.

## Implementation

Work commit: `21f3b477994407587f1ca5a5f5be33be0d963522`

- `internal/cli` owns command, output, error, and exit-code contracts.
- The CLI uses `urfave/cli/v3` version 3.10.1.
- The command constructor receives arguments, streams, build information, and
  the server runner.
- No arguments show help and do not construct the server runtime.
- `serve` is explicit, and the container image supplies it through `CMD`.
- The version command supports stable text and JSON output.
- Usage failures return code 2. Runtime failures return code 1.
- `cmd/starport` owns signals, process streams, final error output, and
  `os.Exit`.

The implementation follows the official v3 action signature and explicit
argument-slice runner. The official documentation identifies v3 as the current
feature line for new development.

## Focused verification

Commands:

```bash
go test ./internal/cli ./cmd/starport -count=1
go test -race ./internal/cli ./cmd/starport -count=1
bash scripts/verify-developer-experience.sh
```

Results:

```text
ok github.com/agentstation/starport/internal/cli
ok github.com/agentstation/starport/cmd/starport
Summary: 17 passed, 22 failed
```

The 22 verifier failures belong to later plan tasks. All six DX3 conditions
pass.

No test in `internal/cli` or `cmd/starport` replaces `os.Args`, `os.Stdout`, or
`os.Stderr`.

## Binary and container verification

The direct checks proved these contracts:

- No arguments print help and return code 0.
- `version --json` produces valid JSON.
- Invalid syntax returns code 2 and prints one diagnostic.
- A missing help topic returns code 2.
- The container image has `CMD ["serve"]`.
- The Linux container returns valid JSON version information.

## Repository gates

These commands passed after the final implementation amendment:

```bash
bash scripts/verify-starmap-ownership.sh
bash scripts/verify-v1-architecture.sh
go test ./...
go vet ./...
make lint
make build
bash scripts/smoke-openrouter-sdks.sh
```

The ownership verifier passed 12 checks. The architecture verifier passed 12
checks. The SDK smoke suite passed raw HTTP and the Python, TypeScript, and Go
OpenRouter clients.

Strict technical-writing lint passed both changed command guides. The glossary
check reported 16 terms and zero errors.

## Autoreview

The isolated `sol` profile used `gpt-5.6-sol` at high reasoning. TruffleHog
reported a clean bundle on each pass.

The first review found two valid error-contract defects. Starport accepted both
findings. The implementation now normalizes built-in help failures to usage
code 2 and prints each diagnostic once.

The second review found the same defect on invalid generated-help syntax.
Starport accepted the finding and replaced the generated help command with a
command that uses the canonical usage handler.

The convergence review reported no actionable finding. It rated the patch
correct at 0.94. It noted that no-argument direct binary invocation no longer
starts the gateway. Decision D4 requires help for this case. The container
passes `serve`, and the owner prefers direct pre-launch changes. Therefore, the
task rejects this last concern by design.
