# DDH1 Starmap catalog contracts

Date: 2026-08-19

Starmap work commit: `99f2ce0d09bbd0b05db296b1d7ede48bcbb02542`

## Outcome

The field-authority table moved from `internal/catalog/authority` to
`pkg/catalogs/authority`. The bounded source decoder moved from
`internal/sources/payload` to `pkg/sources/payload`. No alias or compatibility
package remains at either old path.

The payload package now owns its 16 MiB limit. The repository-wide constants
package refers to that public contract until its remaining callers move to
their concept-owned policies.

## Verification

Focused tests passed for 16 packages. They covered the two new public packages,
catalog and source consumers, reconciliation, acquisition sources, all provider
clients, transport, bootstrap, architecture, and CI workflow contracts.

All six external consumer fixtures passed through
`bash scripts/verify-consumer-deps.sh`. The read-only consumer used 31 of 32
allowed non-standard packages. The pinned-artifact consumer used 32 of 32.

The dependency verifier then reported the expected intermediate result:

```text
SM-D01 PASS: catalogs does not import private authority policy
SM-D04 PASS: catalogs does not import private source payload policy
Summary: 2 passed, 6 failed
```

DDH2 owned the remaining six failures.
