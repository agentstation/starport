# ORP2 proof — request capture pipeline and cost computation

- Branch: `codex/orp-2-usage-capture` (from `codex/orp-1-usage-repo`).
- PR: #128 (base `codex/orp-1-usage-repo`).
- Commit: `feat: capture per-request usage records with Starmap-priced cost`.

## Fail-before evidence

Acceptance tests were written first. Before the implementation landed:

```
$ go test ./internal/proxy/ -run 'Usage|Streaming' -count=1
# github.com/agentstation/starport/internal/proxy [github.com/agentstation/starport/internal/proxy.test]
internal/proxy/usage_capture_test.go: undefined: NewUsageCapture (8 call sites)
FAIL github.com/agentstation/starport/internal/proxy [build failed]
```

## What landed

- `internal/proxy/usage_capture.go`: `UsageCapture` middleware. Wraps the
  gateway outside the proxy middleware chain in `internal/app`, so cache
  hits and every terminal outcome produce one `usage.Record`. Writes are
  asynchronous with a bounded backlog (64) and a 5s per-write timeout;
  overflow and store errors log a warning and never touch the request
  path. `Flush()` is registered on the app lifecycle.
- Cost per decision D2: computed at completion from the request-scoped
  `RoutableSnapshot` offering. Nano-USD over uncached input, cache read,
  cache write (rates fall back to the input rate), and output tokens.
  Missing cost carries `no_route`, `no_usage`, or `no_pricing` — never a
  silent zero. Cache hits cost 0 USD.
- Usage normalization (F8): Anthropic prompt tokens now include cache
  read/write tokens; streams latch `message_start` usage and compose it
  with `message_delta` output tokens (fixes the zero-prompt-token
  stream). OpenAI `prompt_tokens_details.cached_tokens` parses. Gemini
  thought and cached counts map through `convertGeminiUsage`.
- Route evidence: `proxy.ChatCompletionResponse`/`EmbeddingsResponse`
  carry provider, attempts, routing duration, and snapshot; streams
  expose `router.StreamEvidence` behind an `Unwrap()` chain
  (`StreamUnwrapper`) through the caching/logging wrappers.
- F10: `BaseHandler.getRequestID` now uses chi `middleware.GetReqID`
  (the untyped context lookup always returned ""); chat and embeddings
  controllers stamp `req.Protocol`.

## Acceptance evidence (fail-after)

```
$ go test ./internal/proxy/ -run 'Usage|Streaming' -count=1 -v
--- PASS: TestChatCompletionWritesUsageRecord
--- PASS: TestUsageRecordCostFromSnapshotPricing
--- PASS: TestUsageRecordCostUnavailableReason (3 subtests)
--- PASS: TestUsageCaptureFailureDoesNotFailRequest
--- PASS: TestStreamingChatWritesUsageRecordWithProvider
--- PASS: TestStreamingCancellationRecordsCancelledStatus
ok      github.com/agentstation/starport/internal/proxy

$ go test ./internal/providers/connectors/ -run Anthropic -count=1
ok  (includes TestAnthropicStreamReportsPromptTokens: prompt 17 = 10
input + 5 cache read + 2 cache write; output 7; total 24)
```

## Required gates

- Seven `scripts/verify-*.sh` gates: all exit 0.
- `go test ./...`: green (embeddings controller test updated to inject
  the chi request-id key it now reads).
- `go vet ./...`, `make lint` (0 issues), `make build`: green.
- `scripts/smoke-openrouter-sdks.sh`: PASS Python, PASS TypeScript,
  PASS Go.
- Autoreview: Sol (gpt-5.6-sol, thinking=high), branch mode against
  `origin/codex/orp-1-usage-repo`, TruffleHog clean, verdict "patch is
  correct (0.97)", no findings.

## Deviations and open items

- The task's live check ("one chat request against a dev gateway
  produces one listed record") is UNVERIFIED here: no list endpoint
  exists until ORP3. ORP3's proof runs the live transcript over the
  wired capture path.
- `internal/app/app_test.go` and `internal/config/inspection_test.go`
  carry gofmt-only alignment fixes inherited from the parent branch.
