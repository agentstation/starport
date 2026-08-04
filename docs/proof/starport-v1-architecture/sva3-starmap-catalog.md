# SVA3 Starmap catalog proof

Date: 2026-08-03
Status: done

## Fail-before

`TestLegacyMutableCatalogFailBefore` proves that the legacy catalog facts and runtime invalid-model state can diverge. The same global catalog pointer and model record remain active while a separate global map removes that model from discovery.

The first new contract run kept that characterization green. The intended catalog contracts failed because Starport had no control plane, adapter availability, or generation-bound route value.

```text
internal/catalog/control_plane_test.go:17:16: undefined: Open
internal/catalog/control_plane_test.go:21:45: undefined: AdapterAvailability
internal/catalog/control_plane_test.go:112:30: undefined: Route
FAIL github.com/agentstation/starport/internal/catalog [build failed]
ok   github.com/agentstation/starport/pkg/catalog
```

## Change

- `go.mod` now requires Go 1.25 and the tagged `github.com/agentstation/starmap` v0.2.0 release.
- `internal/catalog` retains one immutable Starmap catalog with its generation ID, timestamp, and sequence.
- Runtime adapter registration and configuration use a separate availability revision.
- Provider model observations and exact offering exceptions remain runtime state. They do not mutate Starmap facts.
- Each update derives and atomically publishes one complete routable snapshot. Retained snapshots stay bound to their original generation.
- Each route carries the catalog generation, canonical definition, provider, and exact opaque provider model ID.
- The app opens the Starmap client and catalog control plane before it creates the registry.
- The registry publishes adapter and provider-model observations, and model discovery reads only routable canonical definitions in production composition.
- The router resolves candidates through the routable snapshot and sends the exact provider route ID to connectors.
- Request-time 404 handling changes exact runtime offering availability. Production request paths no longer call the legacy global model-mutation functions.
- Compatibility reads remain for test and transitional compositions that do not yet receive the catalog control plane. SVA8 and SVA9 own their removal.

## Evidence

These commands passed:

```bash
go test ./internal/catalog ./internal/registry ./internal/proxy ./internal/router
go test ./internal/catalog ./internal/registry ./internal/proxy ./internal/router ./internal/app ./internal/server
go test -race ./internal/catalog ./internal/registry
go test ./...
git diff --check
```

The race gate completed with these results:

```text
ok  github.com/agentstation/starport/internal/catalog  33.518s
ok  github.com/agentstation/starport/internal/registry 10.940s
```

The architecture verifier now reports:

```text
PASS V01 Starmap module and Go floor
PASS V02 canonical inference contract
PASS V03 routable snapshot generation contract
PASS V06 provider credential schema and migration contract
PASS V12 full Go test suite
Summary: 5 passed, 7 failed
```

The remaining verifier failures belong to SVA4 through SVA10. They do not contradict the SVA3 acceptance criteria.
