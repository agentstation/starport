# CDP1 credential contract proof

Date: 2026-08-10.

## Scope

CDP1 added a strict, secret-free Starmap credential contract. It covers fields,
profiles, request placement, endpoint bindings, scopes, typed protocol options,
and ordered alternatives for both credential planes.

Starmap now validates provider, alias, profile, field, conventional environment,
and derived product-alias identities before any environment read. JSON, YAML,
deep-copy, catalog generation, and embedded projections preserve the same
contract. Catalog schema version 4 is a direct prelaunch break with no reader
for an earlier schema.

## Fail-before

Command:

```bash
go test ./pkg/catalogs -run '^TestCatalogCredential'
```

Exit status: `1`.

The compiler reported that the catalog did not define the credential types or
the derived environment-name function. Representative errors were:

```text
undefined: DerivedCredentialEnvironmentName
undefined: ProviderCredentials
undefined: ProviderCredentialField
undefined: ProviderCredentialProfile
```

The previous contract could not express the OpenAI organization header or the
terminal Azure API-key and workload-identity alternatives.

## Implementation evidence

- Starmap work commit `3d3428f9` defines the credential contract and tests.
- Starmap work commit `2f858ac4` replaces subscriber test sleeps and
  millisecond liveness assumptions with observable event synchronization.
- `TestCatalogCredentialContract` covers OpenAI header placement, Azure
  alternatives, protected headers, query evidence, unknown profiles,
  ambiguous endpoint bindings, and primitive-owned protocol options.
- Strict JSON and YAML round-trip tests preserve all contract fields.
- Deep-copy tests prove that nested fields, placements, scopes, bindings,
  protocol options, and plane alternatives do not share mutable state.
- Collision tests prove that conventional and derived environment-name
  conflicts reject the catalog before ambient access.

## Verification

- `go test ./pkg/catalogs ./internal/embedded -count=1`: passed.
- `make docs-check`: passed.
- `devbox run golangci-lint run`: passed with zero issues.
- Focused subscriber race tests with `-count=10`: passed.
- `go test ./remote -race -short -count=3`: passed.
- `make verify`: passed under ordinary Go scheduling with no `GOFLAGS`
  override. It passed unit, consumer, file-size, race, vet, performance, lint,
  coverage, documentation, generated-diff, build, version, embedded-catalog,
  isolated-listing, and model-list checks. The final output was
  `repository verification passed`.
- `bash scripts/verify-catalog-driven-providers.sh`: reported
  `Summary: 1 passed, 18 failed`. CDP-V01 passed. The remaining conditions
  belong to later plan tasks.

We interrupted the capped `GOFLAGS=-p=2` run. It is not acceptance evidence.
