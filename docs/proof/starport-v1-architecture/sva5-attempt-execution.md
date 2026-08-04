# SVA5 attempt execution proof

Date: 2026-08-03
Status: done

## Fail-before

The baseline had five independent owners of retry, fallback, or circuit policy.

The connector request helper retried one provider call internally:

```text
internal/providers/connectors/retry.go:10:func doRequestWithRetry(...)
internal/providers/connectors/retry.go:14:for attempt := 0; attempt <= config.MaxRetries; attempt++ {
```

The router owned a second fallback loop, delay policy, and provider circuit state:

```text
internal/router/router.go:205:func (r *modelRouter) RouteWithFallback(...)
internal/router/router.go:362:r.delayBeforeRetry(ctx, i)
internal/router/router.go:563:func (r *modelRouter) isProviderHealthy(provider string) bool
internal/router/router.go:598:func (r *modelRouter) recordProviderFailure(provider string, err error)
```

The proxy owned a separate streaming candidate loop. The public HTTP client
owned another circuit breaker. The Vertex AI connector also changed locations
inside one connector call.

```text
internal/proxy/proxy.go:277:func (p *proxy) ProcessChatCompletionStream(...)
pkg/httpclient/circuit_breaker.go:39:type CircuitBreaker struct {
internal/providers/connectors/vertex_ai.go:411:for i, fallbackLocation := range c.fallbackLocations {
```

These owners could multiply request counts. They also produced incomplete
attempt and availability evidence.

## Change

- `internal/execution` owns the logical-attempt state machine and evidence.
- One immutable route plan supplies streaming and non-streaming execution.
- One total attempt limit bounds retries and fallback routes.
- One total elapsed-time limit includes attempt work and retry waits.
- A connector call makes one outbound HTTP request attempt.
- Provider errors become canonical failures before policy uses them.
- A stream can change routes before its first canonical event only.
- Concurrent stream close can cancel and close a blocked provider read.
- `internal/availability` owns exact offering state and half-open admission.
- The availability key is the provider ID plus the provider model ID.
- The catalog receives immutable availability snapshots as derived state.
- Provider adapters and the HTTP transport own no retry or circuit policy.
- The Vertex AI adapter uses one explicit location for each offering.

The contract covers success, retry, fallback, hard attempt limits,
cancellation, and elapsed-time limits. Stream cases cover start failure,
pre-event fallback, post-event failure, and concurrent close. A fake clock
also proves offering recovery.

## Evidence

These commands passed:

```bash
go test ./internal/execution ./internal/availability ./internal/providers/connectors ./internal/proxy
go test -race ./internal/execution ./internal/availability ./internal/providers/connectors ./internal/proxy
go test -race ./internal/catalog ./internal/router ./internal/architecture
go test ./...
go vet ./...
git diff --check
```

The race checks reported these package results:

```text
ok  github.com/agentstation/starport/internal/execution
ok  github.com/agentstation/starport/internal/availability
ok  github.com/agentstation/starport/internal/providers/connectors
ok  github.com/agentstation/starport/internal/proxy
ok  github.com/agentstation/starport/internal/catalog
ok  github.com/agentstation/starport/internal/router
ok  github.com/agentstation/starport/internal/architecture
```

The V05 fitness check also rejects the removed retry, circuit, and hidden
location symbols in production Go source. The architecture verifier reports:

```text
PASS V01 Starmap module and Go floor
PASS V02 canonical inference contract
PASS V03 routable snapshot generation contract
PASS V04 deterministic route planner contract
PASS V05 attempt state and retry budget contract
PASS V06 provider credential canonical schema contract
PASS V11 import graph architecture fitness
PASS V12 full Go test suite
Summary: 8 passed, 4 failed
```

V07 through V10 remain open for their named plan tasks. They do not conflict
with the SVA5 acceptance criteria.
