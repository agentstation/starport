# DDH5 proxy contracts

Date: 2026-08-19

Starport work commit: `9d5181220728a06310a37cbfd9c794d5dffa4f45`

## Outcome

Proxy configuration and construction now accept the local `CacheManager`
contract and `connectors.LeasingRegistry`. Production proxy code imports
neither the concrete cache adapter nor the concrete registry.

The provider runtime lease now owns the authentication-required fact and the
runtime-unavailable error. Provider discovery projects its response from the
retained Starmap snapshot and lease. The concrete registry no longer owns a
second provider-metadata projection.

The change also removed the unused proxy builder and speculative configuration
options. Repository search found no caller for that surface. The removed
options accepted values but did not change runtime behavior.

## Verification

These focused package suites passed:

```text
go test ./internal/proxy ./internal/providers/connectors ./internal/registry ./internal/app ./internal/router ./internal/cache ./internal/architecture ./internal/server
```

Focused `go vet` passed for the same eight packages. `git diff --check` passed.
The provider-discovery contract test now checks IDs, names, models,
capabilities, authentication policy, and the no-provider-I/O rule from one
retained runtime generation.

The mutation suite stayed green. The real dependency verifier reported:

```text
SP-D01 PASS: proxy does not import the concrete cache adapter
SP-D02 PASS: proxy does not import the concrete provider registry
SP-D03 PASS: proxy exposes the cache behavior contract
SP-D04 PASS: proxy exposes the provider leasing contract
SP-D05 FAIL: app does not import Starmap source selection
SP-D06 FAIL: app does not import Starmap sync options
Summary: 4 passed, 2 failed
```

DDH6 owns the two remaining failures.
