# SVA8 composition and lifecycle proof

Date: 2026-08-03
Status: done

## Fail-before

Production construction had several owners. The command converted some
configuration. The server then created mock storage, repositories, and BYOK
when callers did not supply them. It also created routing and the gateway
service. The registry read provider secrets from the process environment and
constructed connectors. It also selected an implicit mock provider, ran health
checks, and started model-validation goroutines in its constructor.

These paths made a successful constructor result ambiguous. A production
process could start with test dependencies. Resource ownership was split
between the server, registry, cache, storage, and command. Constructor failure
did not have one reverse-order rollback contract.

The server also exposed an unreleased `/openai/v1` deprecation route and a
middleware alias. Configuration advertised provider retry and Vertex fallback
settings that no longer had runtime owners.

## Change

`internal/app` is now the only production composition root. `cmd/starport`
loads one typed configuration value, applies an explicit CLI override, creates
the application, and calls `App.Run`.

`app.New` now constructs these dependencies in order:

1. Storage adapter.
2. Starmap catalog control plane.
3. Identity, provider-credential, and rate-limit repositories.
4. BYOK provider-key service.
5. Explicit provider connectors and connector registry.
6. Optional response cache.
7. Router and gateway service.
8. Optional ChatUI and hot-reload worker.
9. HTTP adapter.

Production construction fails when configuration, storage, the catalog, the
credential master key, provider registrations, or required HTTP ports are
absent. The production path cannot choose a mock. Tests replace bootstrap
factories through an unexported test builder.

The server receives ready gateway, identity, provider-key, rate-limit, and
optional ChatUI ports. It does not import or construct storage, cache, the
registry, or routing. The registry receives explicit connector registrations.
It does not read environment values. `Registry.Start` owns health and model
validation work. Registrations become immutable after start.

`App` records every owned runtime dependency. Constructor rollback and
`App.Close` use the same ledger and close it in reverse construction order.
Close is idempotent. A registry construction error closes all supplied
connectors exactly once, including registrations that were not processed.
Application cancellation drains HTTP and then closes the remaining resources.

Server request timeout, body size, and header size now cross the typed
configuration boundary. The example environment file uses only active
settings and the direct `STARPORT_SECURITY_MASTER_KEY` value. This task
removed the old Ollama environment alias, provider retry settings, Vertex
fallback setting, HTTP deprecation route, and middleware alias. These are
direct pre-launch changes. There are no compatibility branches.

## Contract evidence

`TestProductionCompositionFailsClosed` covers missing storage, catalog,
credential master key, and providers. `TestNewMapsExternalServerConfigurationOnce`
proves the server receives the typed operator values.

The lifecycle tests prove reverse-order, once-only close and cancellation.
The registry tests prove that callers supply connectors and start work
explicitly. They also prove late-register rejection, idempotent close, and
exact connector cleanup after a partial construction failure.

The V08 fitness gate rejects business-service construction and storage,
registry, cache, or router imports in the HTTP adapter. It also rejects mock
selection in production app, registry, and server files and environment reads
inside the registry.

## Verification

These commands passed:

```bash
go test ./internal/app ./internal/config ./internal/server ./internal/registry
go test -race ./internal/app ./internal/server ./internal/registry
go test ./...
go vet ./...
git diff --check
```

The verifier reports:

```text
PASS V01 Starmap module and Go floor
PASS V02 canonical inference contract
PASS V03 routable snapshot generation contract
PASS V04 deterministic route planner contract
PASS V05 attempt state and retry budget contract
PASS V06 versioned concept repository contracts
PASS V07 response cache semantic identity contract
PASS V08 production composition fail-closed contract
FAIL V09 public package boundary contract
FAIL V10 OpenRouter protocol contract
PASS V11 import graph architecture fitness
PASS V12 full Go test suite
Summary: 10 passed, 2 failed
```

V09 and V10 remain open for SVA9. They do not conflict with the SVA8
acceptance criteria.
