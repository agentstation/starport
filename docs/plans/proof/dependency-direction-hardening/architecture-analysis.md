# Starport and Starmap architecture analysis

Date: 2026-08-19

Baselines:

- Starport `main@60f26a6d748b7667a2ed586e38d8a70cf9c161a0`
- Starmap `main@ff5e6cf9beb4cc9047e554659d817d7c21384d6a`

## Outcome

The next architecture campaign must correct dependency direction at two proven
seams. Starmap must make its public catalog tree own its policy and resource
contracts. Starport must make its gateway use cases depend on runtime and cache
contracts, not concrete adapters. Starport must also keep Starmap acquisition
options inside its catalog seam.

The analysis does not support a broad package rewrite. The current concept
packages, immutable catalog generations, request runtime leases, protocol
adapters, and versioned repositories have clear owners and contract tests.

## Method

The audit inspected both repository instruction files, architecture documents,
task ledgers, package trees, module files, recent architecture history, and open
GitHub work. It measured direct package imports with `go list`, searched source
callers with `rg`, inspected the owning implementations, and ran focused tests.

GitHub reported no open pull request or issue in either repository on
2026-08-19. Both worktrees were clean at the pinned baselines.

## Current architecture

Starmap owns catalog facts, source evidence, authority policy, reconciliation,
immutable generation publication, and catalog distribution. Starport consumes
one Starmap generation and adds runtime availability, credentials, routing,
execution, protocols, storage, and HTTP delivery.

The high-level ownership split is correct. The defects are dependency-direction
exceptions inside that split.

## Findings

### A1: Starmap public catalog code imports private implementation packages

Classification: P1, high confidence.

`go list` found eight direct relationships from five packages under
`pkg/catalogs/...` to four packages under the repository-wide `internal/`
tree:

| Importer | Private dependency |
| --- | --- |
| `pkg/catalogs` | `internal/catalog/authority` |
| `pkg/catalogs` | `internal/constants` |
| `pkg/catalogs` | `internal/embedded` |
| `pkg/catalogs` | `internal/sources/payload` |
| `pkg/catalogs/artifact` | `internal/constants` |
| `pkg/catalogs/remote` | `internal/constants` |
| `pkg/catalogs/storage` | `internal/constants` |
| `pkg/catalogs/storage/s3` | `internal/constants` |

The public tree therefore does not own all contracts required to build, decode,
store, distribute, and read one catalog. A public consumer compiles hidden
repository implementation as part of the catalog contract.

The authority table has two real consumers. The reconciler selects accepted
source values, and `pkg/catalogs` derives immutable model definitions with the
same policy. This is a stable catalog policy seam. It should be a catalog-owned
package, not a repository-private implementation detail.

The bounded JSON decoder also has several real consumers. Provider clients,
models.dev parsing, provider source handling, and catalog payload decoding all
use its typed partial-result contract. It is a stable source payload seam.

`catalogs.WithEmbedded()` is the only public catalog constructor that requires
the private embedded filesystem. Starmap is pre-v1. Removing this convenience
option and passing an explicit filesystem keeps bootstrap composition outside
the public domain package.

Catalog resource limits, file modes, lock delay, and transport timeout are
policies of the catalog tree. They do not require the application-wide constants
package.

Recommended repair:

1. Move field authority to `pkg/catalogs/authority`.
2. Move bounded source decoding to `pkg/sources/payload`.
3. Replace `WithEmbedded()` callers with explicit `WithFS(...)` composition.
4. Put catalog resource policy below `pkg/catalogs/internal/`.
5. Add an import verifier that rejects repository-wide private imports from the
   public catalog tree.

### A2: Starport gateway use cases import concrete runtime adapters

Classification: P1, high confidence.

`internal/proxy` imports both `internal/registry` and `internal/cache`.
`proxy.Config`, `proxy.New`, and `proxy.Builder` accept concrete pointers. The
same package already defines the required cache operations as `CacheManager`.
`internal/providers/connectors` already defines runtime leasing as
`LeasingRegistry`.

The concrete imports reverse the intended direction. The composition root must
select the registry and cache implementations. The gateway use case needs only
the two behavior contracts.

Several proxy configuration functions also accept values but do not change
runtime behavior. They include request timeout, validation, routing, security,
and metrics settings. This speculative surface makes the use-case boundary look
larger than the implemented contract.

Recommended repair:

1. Make proxy construction accept `connectors.LeasingRegistry` and
   `CacheManager`.
2. Keep concrete registry and cache construction in `internal/app`.
3. Remove or internalize configuration functions that have no observable
   behavior.
4. Add an import rule that forbids concrete registry and cache imports from
   `internal/proxy`.

### A3: Starport composition knows Starmap acquisition options

Classification: P1, high confidence.

`internal/app` directly imports `pkg/sources` and `pkg/sync`. Its
`catalogRuntime` interface exposes Starmap sync options and results. The app then
selects provider and local sources for catalog refresh.

The application must coordinate lifecycle and complete runtime activation. It
does not need to know how the Starmap acquisition pipeline selects sources. That
policy belongs to `internal/catalog`, which already owns the Starmap client and
acquisition syncer.

Recommended repair:

1. Give the catalog runtime a Starport-owned refresh contract.
2. Move source selection and Starmap sync options into `internal/catalog`.
3. Keep complete runtime activation in `internal/app`.
4. Add an import rule that restricts Starmap acquisition, source, and sync
   packages to `internal/catalog`.

### A4: Architecture records do not match current releases and boundaries

Classification: P2, high confidence.

Starport `docs/TASKS.md` still names Starmap v0.4.1 as the fact owner. Starport
currently consumes v0.6.0. Its architecture document was last updated before
the latest package-ownership and provider work.

Starmap `docs/ARCHITECTURE.md` is 2,406 lines. It repeats test commands and
implementation examples. It also names the current private authority path.
The document is useful as a reference, but it does not provide a compact resume
surface for the dependency rules in this campaign.

Recommended repair:

1. Update the affected ownership and package sections after code moves.
2. Keep detailed behavior in package documentation and tests.
3. Make the new import rules executable in each repository's owned verifier.

## Audited patterns to preserve

The campaign must preserve these current patterns:

- Starmap publishes immutable catalog generations and returns caller-owned
  collection copies.
- Starmap keeps acquisition explicit and outside the small root client.
- Starmap catalog storage, artifacts, and remote protocol remain separate child
  behaviors.
- Starport derives provider and model facts from one Starmap snapshot.
- Starport request leases retain a matching catalog snapshot and connector set.
- Starport owns inference credentials. Starmap owns catalog acquisition
  credentials.
- Starport protocol packages convert once through canonical inference and
  failure types.
- Concept repositories own their persisted schemas and storage contracts.

## Rejected broad changes

### Split `pkg/catalogs` by data type

The package has a large public surface, but its files remain below the project
decomposition threshold. Recent work established one catalog package tree.
The package owns one coherent domain. This audit found reverse dependencies,
not a need to split every model and provider type.

### Remove Starport local catalog acquisition

The serving process can compose Starmap acquisition without owning its policy or
credentials. The evidence supports an import-boundary repair. It does not show a
required product behavior break or a separate catalog service requirement.

### Replace Starport runtime leases

The registry and catalog use separate atomic pointers, but request leases retain
one complete provider runtime generation. Existing tests cover invalid
candidates, replacement, connector draining, and credential rotation. The audit
found no request-visible generation split.

### Rename every high-fanout package

`internal/app` and Starmap CLI composition have the highest outbound fanout.
They are composition roots, which normally have high fanout. A rename would not
repair an ownership defect.

## Acceptance evidence

The campaign is complete when:

- Starmap reports zero direct imports from `pkg/catalogs/...` to
  `github.com/agentstation/starmap/internal/...`.
- The authority and payload contracts each have at least two real consumers and
  contract tests.
- Starport `internal/proxy` imports neither `internal/cache` nor
  `internal/registry`.
- Starport `internal/app` imports no Starmap acquisition, source, or sync
  package.
- Each repository runs its new dependency verifier in its standard verification
  workflow.
- Focused, full, race, lint, build, and repository architecture checks pass.
- Both implementation changes merge through pull requests.
