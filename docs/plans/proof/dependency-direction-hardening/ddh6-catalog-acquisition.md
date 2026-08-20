# DDH6 catalog acquisition boundary

Date: 2026-08-19

Starport work commit: `572482d51a9d656bd08331ae0150c7ca00442cea`

## Outcome

The app catalog contract now accepts a Starport-owned refresh-candidate method.
It exposes no Starmap source, acquisition, sync option, or sync result type.

The local catalog runtime owns provider and local source selection, the sync
timeout option, the default timeout, and the bounded context. The remote
runtime implements the same method by reading its verified subscriber state.
Remote publication and retry work stays in its existing lifecycle.

The acquisition credential integration test moved from app composition to the
catalog seam. The dependency verifier now checks production, internal test, and
external test imports. Proxy tests use a runtime contract fixture instead of a
concrete registry.

## Verification

Focused tests passed for catalog, app, all provider packages, registry, config,
diagnosis, architecture, and proxy. Focused `go vet` passed for the same
packages. `git diff --check` passed.

The catalog contract tests prove the standard source list, explicit and default
timeouts, bounded context, remote candidate behavior, catalog-acquisition
credential isolation, and generation publication.

The mutation suite passed. The real dependency verifier reported:

```text
SP-D01 PASS: proxy does not import the concrete cache adapter
SP-D02 PASS: proxy does not import the concrete provider registry
SP-D03 PASS: proxy exposes the cache behavior contract
SP-D04 PASS: proxy exposes the provider leasing contract
SP-D05 PASS: app does not import Starmap source selection
SP-D06 PASS: app does not import Starmap sync options
Summary: 6 passed, 0 failed
```

The complete v1 architecture verifier also passed:

```text
Summary: 12 passed, 0 failed
```
