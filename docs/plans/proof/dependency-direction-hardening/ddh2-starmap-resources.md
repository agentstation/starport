# DDH2 Starmap catalog resource ownership

Date: 2026-08-19

Starmap work commit: `1736653bdb719266f368c95606fb5613360ae374`

## Outcome

Catalog limits, file modes, the store lock delay, and the remote HTTP timeout
now live in `pkg/catalogs/internal/resourcepolicy`. The complete catalog tree,
including tests, has no import to a repository-wide private package.

The change removed `catalogs.WithEmbedded` and `catalogs.NewEmbedded`. The
bootstrap composition now passes a caller-owned embedded filesystem through
`catalogs.WithFS`. Author ID parsing no longer loads global embedded state.
Catalog-specific alias resolution stays on `Authors.Resolve`.

## Verification

The verifier mutation suite passed. The real repository reported:

```text
SM-D01 PASS: catalogs does not import private authority policy
SM-D02 PASS: catalogs does not import repository-wide constants
SM-D03 PASS: catalogs does not import the private embedded filesystem
SM-D04 PASS: catalogs does not import private source payload policy
SM-D05 PASS: catalog artifacts do not import repository-wide constants
SM-D06 PASS: catalog remote transport does not import repository-wide constants
SM-D07 PASS: catalog storage does not import repository-wide constants
SM-D08 PASS: catalog S3 storage does not import repository-wide constants
Summary: 8 passed, 0 failed
```

Focused tests passed for the full catalog tree, bootstrap, acquisition pipeline,
embedded-provider contracts, Anthropic and OpenAI provider contracts, and the
dependency-check command. Focused `go vet` passed for the changed production
seams. `git diff --check` passed.
