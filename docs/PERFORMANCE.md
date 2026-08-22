# Gateway overhead

Starport measures the latency it adds to every request and shows that
number to the operator. The claim in the README — under 50 ms p99 gateway
overhead — is enforced by a CI benchmark, not asserted.

## Definition

Gateway overhead is the total request handling time minus the time spent
waiting on the upstream provider. The overhead timer starts when the chat
controller accepts the request. The router marks every upstream interval:
the non-streaming `Chat` call, the `ChatStream` open, and every stream
receive. What remains is Starport's own work: decode, validation, route
planning, credential resolution, execution management, and encode.

The timer lives in `internal/execution` (`OverheadTimer`); the header
constant and context facade live in `internal/proxy`.

## Surfaces

- Every proxied chat response carries `x-starport-overhead-ms`. On a
  streamed response the header reports the overhead at header flush;
  the complete number lands in the usage record.
- Usage records persist `overhead_ms` per request, and `ttft_ms` (time to
  first stream event) for streamed requests.
- The console Usage page shows overhead and TTFT columns per request.
- The console Overview shows Starport overhead p50/p99 from
  `GET /api/v1/admin/metrics` (`overhead.p50/p95/p99`, nearest-rank over
  the 24-hour sample window).

## Benchmark harness

`scripts/benchmark-overhead.sh` runs `TestGatewayOverheadBenchmark`
(`internal/server/controllers/overhead_benchmark_test.go`): 200 sequential
requests through the real chat pipeline — controller decode, proxy
validation and transform, route planning, execution loop, encode — against
a mock upstream connector that sleeps 20 ms per call. The test reads
`x-starport-overhead-ms` from every response, computes nearest-rank p50
and p99, prints both, and fails when p99 exceeds 50 ms.

CI runs the script on every push. A regression that pushes gateway
overhead past the bound fails the build.

Excluded from the harness measurement: network transport to the gateway,
TLS, response cache lookups (no cache is configured in the harness), and
provider inference time. A contract test in `internal/router`
(`TestRouteWithFallbackOverheadExcludesUpstreamDelay`) separately proves a
500 ms upstream sleep leaves measured overhead under 50 ms.

## Reproduce locally

```bash
bash scripts/benchmark-overhead.sh
```
