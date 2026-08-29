# Router

The `router` package adapts gateway requests to the pure route planner and the
attempt executor. It does not own model capabilities, prices, context limits,
or provider offerings. Starmap owns those facts through one immutable routable
snapshot.

## Routing behavior

- A single model selects matching provider offerings from the snapshot.
- A `models` array sets explicit model order and fallback order.
- `openrouter/auto` lets the planner consider any routable model in the
  snapshot. In a mixed array, explicit models stay ahead of the automatic
  fallback set.
- Provider `order`, `only`, and `ignore` rules are request policy.
- Account model and provider restrictions are hard constraints.
- Measured latency, Starmap price facts, required capabilities, context size,
  and provider affinity determine the stable order within one model rank.
- The executor owns the total retry and fallback budget for streaming and
  non-streaming calls.

## Ownership

- `internal/catalog` publishes the generation-consistent Starmap snapshot.
- `internal/routing` plans immutable attempts without network or mutable state.
- `internal/availability` owns runtime offering health.
- `internal/execution` runs the plan within one budget.
- `internal/providers/connectors` adapts each provider transport.

Run the package contracts with:

```bash
go test ./internal/router ./internal/routing
```
