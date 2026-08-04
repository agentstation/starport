# SVA4 route planner proof

Date: 2026-08-03
Status: done

## Fail-before

The baseline router directly imported connector interfaces and executed provider calls inside fallback.

```text
github.com/agentstation/starport/internal/providers/connectors
internal/router/router.go:304: resp, err = connector.Chat(ctx, &reqCopy)
```

The first pure-planner contract then failed to compile because `internal/routing` had no planner or route-policy concepts.

```text
internal/routing/planner_test.go:13:14: undefined: NewPlanner
internal/routing/planner_test.go:15:14: undefined: Request
internal/routing/planner_test.go:18:15: undefined: ProviderPolicy
internal/routing/planner_test.go:219:25: undefined: Snapshot
FAIL github.com/agentstation/starport/internal/routing [build failed]
```

## Change

- `internal/routing` owns transport-free routes, requirements, tenant and provider policy, candidates, rejections, attempts, selection evidence, and immutable plans.
- The planner applies hard availability, health, tenant, provider, capability, and context constraints before it ranks eligible routes.
- Stable ranking preserves model fallback order, explicit provider order, affinity, cost, latency, and lexical tie-breaking.
- Tenant model overrides are part of planning policy. An allowed override source also authorizes its reviewed target.
- The planner validates catalog generation consistency, route identity, duplicate routes, token counts, prices, context limits, and latency values.
- No clock, random source, connector, HTTP type, or network call enters the planner package.
- The router adapter captures one catalog snapshot before it calls the planner. It also captures health, latency, affinity, connector presence, capabilities, limits, and prices.
- Catalog-backed streaming and non-streaming selection now consume ordered planner attempts. The existing router still executes those attempts until SVA5 replaces execution ownership.
- `internal/architecture` now enforces the first import-graph rules for routing, catalog, inference, and failure seams.

## Evidence

These commands passed:

```bash
go test ./internal/routing
go test ./internal/routing -run '^$' -fuzz '^FuzzRoutePlanner$' -fuzztime=10s
go test ./internal/routing -run '^$' -bench '^BenchmarkRoutePlanner$' -benchmem
go test ./internal/architecture -run '^TestImportGraphArchitecture$'
go test -race ./internal/routing ./internal/router
go test ./...
git diff --check
```

The final 10-second fuzz gate ran 1,899,351 executions and retained 176 interesting inputs. The benchmark reported this result on an Apple M2 Max:

```text
BenchmarkRoutePlanner-12  1000000  1192 ns/op  1912 B/op  18 allocs/op
```

The `internal/routing` production package imports only `errors`, `fmt`, `sort`, `strings`, and `time`. The technical-writing linter reported zero diagnostics for the five SVA4 source and test paths.

The architecture verifier now reports:

```text
PASS V01 Starmap module and Go floor
PASS V02 canonical inference contract
PASS V03 routable snapshot generation contract
PASS V04 deterministic route planner contract
PASS V06 provider credential schema and migration contract
PASS V11 import graph architecture fitness
PASS V12 full Go test suite
Summary: 7 passed, 5 failed
```

The remaining verifier failures belong to SVA5 through SVA10. They do not contradict the SVA4 acceptance criteria.
