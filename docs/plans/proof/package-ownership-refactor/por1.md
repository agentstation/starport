# POR1 Starmap package ownership proof

POR1 moves six internal support packages to concept-owned paths. It preserves
their exported behavior and does not add a compatibility package.

## Package layout

| Previous path | Current path | Package name |
|---|---|---|
| `internal/bootstrapmanifest` | `internal/bootstrap/manifest` | `manifest` |
| `internal/embeddedbudget` | `internal/bootstrap/budget` | `budget` |
| `internal/sourcepayload` | `internal/sources/payload` | `payload` |
| `internal/testcatalog` | `internal/test/catalog` | `catalog` |
| `internal/testlogging` | `internal/test/logging` | `logging` |
| `internal/providers/testhelper` | `internal/test/providerfixture` | `providerfixture` |

Callers use explicit aliases such as `sourcepayload`, `testcatalog`, and
`testlogging` where the qualified name makes the call clearer. The source
packages use their directory base names. The old directories are absent.

`internal/architecture.TestApprovedInternalPackageLayout` checks the approved
directories and package declarations. `scripts/verify-package-layout.sh`
checks current source, scripts, workflows, and durable documentation for stale
paths. Its regression test proves that current stale references fail and
archived review evidence does not fail.

## Preserved behavior

The focused ordinary and race suites passed with normal uncapped Go
scheduling:

```text
go test -count=1 ./internal/bootstrap/... ./internal/sources/... ./internal/test/... ./internal/architecture ./cmd/starmap-bootstrap-manifest ./cmd/starmap-embedded-budget
go test -race -count=1 ./internal/bootstrap/... ./internal/sources/... ./internal/test/... ./internal/architecture ./cmd/starmap-bootstrap-manifest ./cmd/starmap-embedded-budget
```

The focused race run included the bootstrap command at 89.786 seconds and the
embedded budget command at 26.955 seconds. Bootstrap took 62.782 seconds. The
budget package took 87.446 seconds, and models.dev took 72.672 seconds. Existing tests
continue to compare canonical report fields, unchanged manifest bytes, payload
checksums, fixture metadata, partial-source reports, and schema-drift evidence.

The full ordinary suite passed across all 86 current package suites. The full
repository verifier passed, including:

- all pure-Go external consumer compositions.
- the full `CGO_ENABLED=1` race suite without a scheduler cap.
- vet and golangci-lint 2.12.2 with zero issues.
- catalog performance at 8.799 to 8.971 ns/op, 0 B/op, and 0 allocations.
- every critical coverage floor.
- generated documentation and OpenAPI consistency.
- catalog validation for 14 providers, 104 authors, 611 models, and all
  cross-references.
- isolated credential-free provider listing and CLI smoke tests.

The moved payload package exposed one path-based assumption in the external
consumer dependency guard. The public read-only surface already depended on
the same pure payload implementation through `pkg/sources`. Only its internal
path changed. The corrected guard permits exactly
`internal/sources/payload` while it continues to reject the broader
`internal/sources` implementation family. A workflow contract test protects
that exact exception. All six pure-Go compositions then passed within their
existing dependency limits.

`make catalog-generation-check`, `make docs-check`, both package-layout
scripts, shellcheck, `git diff --check`, and the full `make verify` gate passed.

## Campaign state

The campaign verifier now reports:

```text
PASS POR-V01 Starmap approved package layout
PASS POR-V02 Starmap source-payload and bootstrap behavior
Summary: 2 passed, 7 failed
```

POR-V03 through POR-V09 remain red because their owning tasks have not run.
This is the expected pre-merge campaign state for POR1.
