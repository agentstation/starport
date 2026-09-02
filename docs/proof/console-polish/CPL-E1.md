# CPL-E1 proof: honest system info and a webhook summary route

Branch `codex/cpl-e1`. Base: the CPL-D4 squash `5000da4`.

## What changed

| Owner | Change |
| --- | --- |
| `internal/events/dispatcher.go` | Owns the delivery state a reader sees: `Stats` returns the redacted receivers, the queue depth against its capacity, and the dead letter count. `Types` lists the seven event names in guide order. `RedactEndpoint` drops the userinfo and the query from a receiver URL. |
| `internal/usage/sink.go` | The `Sink` contract gains `Dropped`, and the batching sink retains its own drop count beside the metrics callback. |
| `internal/server/controllers/admin.go` | Owns `BuildInfo`, `Deployment`, and `WebhookReporter` with one option each. `SystemInfo` states the build stamp, the start time, the uptime, both store modes, the telemetry surfaces, the guardrail settings, the retention windows, and the webhook summary. `Webhooks` serves the summary alone. |
| `internal/server/controllers/controllers.go` | The controller config carries `Build`, `Deployment`, and `Webhooks`. The hard-coded `Version` field is gone, and the health controller reads the stamped version. |
| `internal/server/config.go`, `server.go`, `routes.go` | The server config carries `Build`. The dependencies carry `Deployment` and `Webhooks`. The admin router mounts `GET /api/v1/admin/webhooks`. |
| `internal/app/runtime.go`, `app.go`, `development.go` | `WithBuildInfo` is a public runtime option. `New` records the start time. The builder retains the usage sink, projects the configuration onto `Deployment`, and hands the dispatcher to the admin summary under the same nil rule the emitter keeps. `NewDevelopment` accepts options. |
| `cmd/starport/run.go` | Both `serve` and `dev` pass the linker values through `WithBuildInfo`. |
| `docs/OPERATOR-GUIDE.md`, `docs/ARCHITECTURE.md` | The guide describes both shapes with a field table and a sample answer. The architecture route list names the new route. |

## Honesty rules

| Fact | Source | When unknown |
| --- | --- | --- |
| Version, commit, build time | Linker variables | `dev` |
| Start time, uptime | The clock at `New` | `unavailable` |
| Store modes, metrics mode | Loaded configuration with its defaults | `unavailable` |
| Traces | The configured collector host alone | `null` |
| Usage export | The sink kind and its own drop count | `kind: off` |
| Receiver URLs | `events.RedactEndpoint` | Empty list, `configured: false` |

A hand-built controller states `unavailable` rather than a plausible guess. The controllers package never reads the configuration or the environment. The composition root supplies plain values and two live readers.

## Counts

| Measure | Before | After |
| --- | --- | --- |
| Go tests in `internal/server/controllers` | 116 | 120 |
| Go tests in `internal/events` | 8 | 10 |
| Verifier | 29 passed, 19 failed | 31 passed, 17 failed |

## Fail-before

At `e1ecafc` (origin/main) in a fresh worktree the verifier reported `FAIL CPL-V30` and `FAIL CPL-V31` with 27 passed and 21 failed. The copied test files did not compile there, because the tree lacked `Stats`, `RedactEndpoint`, `Dropped`, `WithBuildInfo`, `BuildInfo`, and `WithDeployment`.

## Tests added

| File | Test |
| --- | --- |
| `internal/events/dispatcher_test.go` | `TestDispatcherStatsReportQueueDepthAndDeadLetters` reads a redacted receiver, the capacity, and two dead letters, and the nil zero. `TestRedactEndpoint` covers four URL shapes. |
| `internal/usage/sink_test.go` | The down-target test also asserts `Dropped` returns 2. |
| `internal/server/controllers/admin_test.go` | `TestSystemInfoReportsBuildVersion` asserts the version, commit, build time, start time, uptime, store modes, telemetry, guardrails, and retention. `TestSystemInfoNamesAnUnstampedBuild` asserts `dev` and `unavailable`. `TestAdminWebhooksSummaryRedactsURL` reads a real dispatcher and finds no credential in the body. `TestAdminWebhooksSummaryWithoutAReceiver` reads the unconfigured summary. `TestAdminHandler_SystemInfo` keeps passing. |

## Commands

| Command | Result |
| --- | --- |
| `gofmt -l internal cmd` | Clean for the changed files. |
| `go vet ./...` | Clean. |
| `go test ./...` | Every package passed. |
| `make lint` | 0 issues. |
| `bash scripts/verify-console-polish.sh` | 31 passed, 17 failed. V30 and V31 pass. |
| `bash scripts/verify-*.sh` (23 gates, release build gates excluded) | All 23 passed. |
| Technical-writing lint on the guide | The new sections add no diagnostic. The file holds its 48 prior diagnostics. |
