# CDP5 Starport dynamic provider configuration

Status: `done`

Work commit: Starport `72964ba`

## Fail-before evidence

- `ProvidersConfig` contained eight named Go fields. A provider needed a source
  edit before Starport could read its configuration.
- The loader decoded `STARPORT_PROVIDERS_*` names before it opened Starmap.
- Fireworks had no Starport configuration slot even though Starmap declared
  its credential contract.
- Configuration validation contained explicit Google Vertex and Azure OpenAI
  provider IDs.
- Setup and first-run checks wrote the removed provider namespace.

## Catalog-keyed resolution

`ProvidersConfig` is now a map keyed by exact Starmap provider ID. It has no
provider fields or membership list. The loader keeps the ordered, read-only
environment and configuration-file source. It does not read a provider value
until application composition opens the active Starmap catalog.

Application composition resolves provider configuration from the exact
catalog snapshot after an optional startup refresh and before connector
construction. Diagnosis uses the catalog state that it loads. Manually
supplied test configurations remain exact provider-keyed entries.

The resolver applies this ambient order for each catalog credential field:

1. The conventional environment names in Starmap, in declared order.
2. The derived `STARPORT_<PROVIDER_ID>_<FIELD_ID>` alias.
3. The catalog default, when one exists.

An invalid selected value is terminal. The resolver does not continue to a
lower-priority source. It validates the complete conventional and derived
alias namespace before the first value lookup. Cross-category collisions also
fail, such as one field's conventional name matching another field's derived
name.

The selected catalog profile supplies its authentication primitive and
endpoint bindings. URL bindings also supply a complete base URL when the
provider base is the matching catalog template. Credential material,
renewal, explicit references, and direct sources remain owned by CDP5.1.

## Breaking configuration change

Production Go source contains no `STARPORT_PROVIDERS_*` name or compatibility
path. Local setup now writes `OPENAI_API_KEY` for OpenAI and
`OLLAMA_BASE_URL` for Ollama. The Docker Compose file, integration target, and
isolated first-run smoke test use `OPENAI_API_KEY`.

The named tests prove these contracts:

- `TestCatalogCredentialEnvironmentPrecedence` proves conventional-before-
  product selection and terminal invalid values.
- `TestCredentialAliasCollisionsFailBeforeConnectorConstruction` proves zero
  environment reads before collision rejection.
- `TestCatalogOnlyProviderEnvironmentResolvesWithoutSourceRoster` resolves
  `FIREWORKS_API_KEY` from the Starmap record without a Starport provider
  configuration field.
- The loader test proves that provider environment reads wait for catalog
  resolution.

## Verification

These ordinary checks passed:

- `go test ./internal/config ./internal/app ./internal/catalog`.
- `go test ./...` across all Starport packages.
- `go vet ./...`.
- `make lint` with zero issues.
- `bash scripts/smoke-first-run.sh`.
- `git diff --check`.

This uncapped race check passed:

```text
go test -race ./internal/config ./internal/app ./internal/catalog
```

The race run completed `internal/config` in 28.166 seconds and `internal/app`
in 26.008 seconds. No race report occurred. No command used `GOFLAGS`, `-p`, a
scheduler cap, or a timeout change.

The campaign verifier reported:

```text
Summary: 6 passed, 13 failed
```

CDP-V04, CDP-V05, and CDP-V08 became green. CDP5.1 owns explicit references,
credential material, source conformance, rotation, and the warmed cache.
CDP6 owns the remaining compiled provider-adapter roster and request-bound
credential application.
