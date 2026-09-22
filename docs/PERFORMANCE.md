# Gateway overhead

The current timer measures part of chat request processing.
Its CI threshold is a component regression guard.
It does not establish complete gateway latency or a production p99 limit.

## Definition

The chat controller starts its timer after request decoding and routing-policy extraction.
Authentication, rate limits, and budget checks precede that timer.
The header samples the timer before final response encoding.

The router subtracts complete connector calls, including local work around network I/O.
Those intervals include provider delay and local connector work.
An integer-millisecond value of zero does not mean that Starport added no latency.

The timer lives in `internal/execution` (`OverheadTimer`). The header
constant and context facade live in `internal/proxy`.

## Surfaces

- Every proxied chat response carries `x-starport-overhead-ms`. On a
  streamed response the header reports the overhead at header flush.
  The usage record stores a later sample of the same partial timer.
- Usage records persist `overhead_ms` per request, and `ttft_ms` (time to
  first stream event) for streamed requests.
- The console Usage page shows overhead and TTFT columns per request.
- The console Overview shows Starport overhead p50/p99 from
  `GET /api/v1/admin/metrics` (`overhead.p50/p95/p99`, nearest-rank over
  the 24-hour sample window).

## Benchmark harness

`scripts/benchmark-overhead.sh` runs `TestGatewayOverheadBenchmark`
(`internal/server/controllers/overhead_benchmark_test.go`): 200 sequential
requests through a controller test harness against
a mock upstream connector that sleeps 20 ms per call. The test reads
`x-starport-overhead-ms` from every response, computes nearest-rank p50
and p99, prints both, and fails when p99 exceeds 50 ms.

CI runs the script on every push.
A regression that exceeds this component threshold fails the build.

The measurement excludes authentication, rate limits, budgets, request decoding, final encoding, network transport to the gateway, and TLS.
The harness also has no response cache. The router subtracts complete connector intervals.
A contract test in `internal/router`
(`TestRouteWithFallbackOverheadExcludesUpstreamDelay`) separately proves a
500 ms upstream sleep leaves measured overhead under 50 ms.

## Reproduce locally

```bash
bash scripts/benchmark-overhead.sh
```

## Complete HTTP baseline

`internal/app/performance_test.go` composes the real application with persistent Badger, SQLite, and local file storage.
It enables authentication, rate limits, a key budget, usage capture, and Prometheus metrics.
The catalog and provider adapters use their production implementations.
Only the provider service uses a controlled loopback fixture.
The fixture checks the destination, credential, model, message, and output limit.

The harness compares equivalent direct and proxied requests in alternating order.
Each sample retains elapsed nanoseconds, handler completion, first byte, first content event, and per-event forwarding delay.
It subtracts only measured upstream sleep time.
It computes each paired difference before it computes percentiles.
The result still includes client and loopback transport costs.

Run the measurement with an absolute output path:

```bash
STARPORT_FULL_PATH_SAMPLES=100 \
STARPORT_FULL_PATH_REPORT=/tmp/starport-full-path.json \
  go test -count=1 -run '^TestFullPathMeasurementBaseline$' ./internal/app
```

Run the allocation benchmark:

```bash
go test -run '^$' -bench '^BenchmarkFullPathHTTP$' -benchmem ./internal/app
```

Benchmark allocations include the client and controlled provider.
Use profiles to attribute gateway allocations before making a gateway resource claim.
The normal test suite also checks authentication refusal before any provider request.

This baseline uses one tenant, one model, pooled HTTP/1.1 connections, and disabled response caching.
It excludes TLS, DNS, active maintenance loops, retries, and shared backends.
It does not establish production percentiles, cold-start performance, or performance under saturation.
The production qualification must exercise those conditions separately.

## Production engineering targets

The versioned [performance profile](performance-targets-v1.json) defines targets for the planned production release.
Current results do not qualify these limits.
CSP22 owns release qualification on identified, dedicated runners.

| Warm request limit | Local Badger and SQLite | Three-replica Valkey and PostgreSQL |
|---|---:|---:|
| Paired added latency p50 | 0.5 ms | 1.5 ms |
| Paired added latency p95 | 1 ms | 2.5 ms |
| Paired added latency p99 | 2 ms | 4 ms |
| Paired added latency p99.9 | 5 ms | 8 ms |
| First-byte and first-token added latency p99 | 2 ms | 4 ms |
| Per-event forwarding p99 | 0.25 ms | 0.25 ms |

These limits apply to valid, warm policy and credentials, pooled connections, and disabled or missed response caches.
Fleet qualification requires store round-trip p99 at or below 0.5 ms.
Cache hits, cold loads, failure recovery, and paid auxiliary operations require separate results.
Required budget admission remains in the measured request path.
An unknown required budget still causes a retryable refusal.

The standard workload uses an 8 KiB request, 10,000 catalog routes, and 1,000 tenants.
It offers 200 ordinary requests or 50 streams per second per replica.
Each replica permits 32 concurrent requests in this workload.
Ordinary response content is 2 KiB.
Each stream contains 256 events with 64 content bytes per event.

Controlled usage fields specify 1,024 prompt tokens and 256 completion tokens.
These fields do not assert a tokenizer result for the fixture text.

Gateway allocation targets are 256 KiB and 1,500 allocations per request, plus 4 KiB and 32 allocations per content event.
Profiles must separate gateway work from the client and controlled provider.
The profile also bounds retained stream memory, heap, RSS, CPU, GC, queues, caches, generations, and cancellation.
These resource limits never permit expired credentials, stale authority, or skipped admission checks.

Qualification requires three runs per variant and at least 100,000 samples and 600 seconds per run.
At 50 streams per second, the sample floor requires at least 2,000 seconds per run.
Use open-loop load and retain raw samples, negative differences, and exact artifact identities.
Calculate differences per pair before percentiles.
Report 95% confidence intervals and require each upper bound to meet its target.
Investigate repeatable regressions above 10%, even when an absolute target passes.

The profile names the required credential, authority, cache, and connection variants.
It also names large-input, long-stream, retry, refresh, outage, saturation, slow-client, cancellation, and rotation exercises.
Native functional archive checks cover all six published platforms.
Initial numeric qualification covers Linux x64 and ARM64, macOS ARM64, and Windows x64.
Other latency claims require a profile revision and evidence.
