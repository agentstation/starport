# DDH4 Starport dependency baseline

Date: 2026-08-19

Starport work commit: `ec1376aba6a8fcc2dd1b6db6e4be8f5c70766f74`

## Outcome

Starport now has six executable dependency-direction conditions. They reject
the four reversed package relationships and require proxy construction to use
the existing cache and provider-leasing contracts.

The verifier is part of the standard v1 architecture gate. Its isolated
mutation suite proves that each condition fails without changing another
condition.

## Red baseline

The repository reported:

```text
SP-D01 FAIL: proxy does not import the concrete cache adapter
  internal/proxy imports forbidden package github.com/agentstation/starport/internal/cache
SP-D02 FAIL: proxy does not import the concrete provider registry
  internal/proxy imports forbidden package github.com/agentstation/starport/internal/registry
SP-D03 FAIL: proxy exposes the cache behavior contract
  proxy Config must declare CacheManager with contract type CacheManager
SP-D04 FAIL: proxy exposes the provider leasing contract
  proxy Config must declare Registry with contract type connectors.LeasingRegistry
SP-D05 FAIL: app does not import Starmap source selection
  internal/app imports forbidden package github.com/agentstation/starmap/pkg/sources
SP-D06 FAIL: app does not import Starmap sync options
  internal/app imports forbidden package github.com/agentstation/starmap/pkg/sync
Summary: 0 passed, 6 failed
```

The standard v1 architecture verifier reached the new gate and reported V11
as failed. Its other 11 conditions passed, including the full Go test suite.
The focused architecture package also passed.

## Verification

```text
bash -n scripts/verify-dependency-direction.sh scripts/test-dependency-direction-verifier.sh
shellcheck scripts/verify-dependency-direction.sh scripts/test-dependency-direction-verifier.sh
bash scripts/test-dependency-direction-verifier.sh
go test ./internal/architecture -count=1
bash scripts/verify-v1-architecture.sh
```

The first four commands passed. The final command failed only at V11 with the
required red dependency report. This fail-before state is intentional for
DDH4. DDH5 owns SP-D01 through SP-D04. DDH6 owns SP-D05 and SP-D06.
