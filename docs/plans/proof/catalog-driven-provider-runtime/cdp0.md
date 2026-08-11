# CDP0 red verifier proof

Date: 2026-08-10.

## Scope

CDP0 added
`scripts/verify-catalog-driven-providers.sh` without a production source
change. The verifier owns 19 terminal condition IDs. It checks both pinned
repositories.

The shared production scans include Starport configuration, composition,
setup, diagnosis, BYOK, and registry code. They also include Starmap shared
authentication, CLI, provider registry, and provider-source code. The scans
exclude protocol codecs, provider-specific transport packages, tests,
fixtures, documentation, and declarative YAML.

The verifier owns these source-conformance vector IDs:

```text
static
default_chain
version
expiry
lease
cancellation
concurrency
denial
redaction
rotation_in_place
rotation_atomic_replace
rotation_symlink_swap
rotation_mounted_replace
rotation_agent_rerender
```

Each product-owned conformance test must run this complete set.

## Fail-before command

```bash
bash scripts/verify-catalog-driven-providers.sh 2>&1
```

Exit status: `1`.

```text
FAIL CDP-V01 credential schema preserves one strict contract
missing test: TestCatalogCredentialContract
FAIL CDP-V02 acquisition and inference credential values stay isolated
missing test: TestCatalogCredentialPlanesAreIsolated
FAIL CDP-V03 every provider YAML record uses the credential schema
missing test: TestEmbeddedProviderCredentialSchemaContract
FAIL CDP-V04 both products implement catalog-declared environment precedence
missing test: TestCatalogCredentialEnvironmentPrecedence
FAIL CDP-V05 alias collisions fail before reads or connector construction
missing test: TestCredentialAliasCollisionsFailBeforeEnvironmentReads
FAIL CDP-V06 explicit references precede ambient sources and fail closed
missing test: TestExplicitCredentialReferencesPrecedeAmbientSources
FAIL CDP-V07 both products pass the same source-conformance vectors
missing test: TestCredentialSourceConformance
FAIL CDP-V08 old Starport provider environment names are absent
/Users/jack/src/github.com/agentstation/starport/internal/setup/service.go:29:	OpenAIProviderCredentialEnvironment = "STARPORT_PROVIDERS_OPENAI_API_KEY"
/Users/jack/src/github.com/agentstation/starport/internal/setup/service.go:433:		values["STARPORT_PROVIDERS_OLLAMA_ENABLED"] = "true"
FAIL CDP-V09 Starport production selection has no provider roster
missing test: TestStarportProductionHasNoProviderRoster
FAIL CDP-V10 Starmap acquisition selection has no provider roster
missing test: TestStarmapAcquisitionHasNoProviderRoster
FAIL CDP-V11 shared production zones contain no provider or model facts
/Users/jack/src/github.com/agentstation/starport/internal/setup/service.go:29:	OpenAIProviderCredentialEnvironment = "STARPORT_PROVIDERS_OPENAI_API_KEY"
FAIL CDP-V12 transport and authentication registries use primitives only
missing test: TestTransportAuthenticationRegistriesUsePrimitives
FAIL CDP-V13 connectors retain no credential and requests stay isolated
missing test: TestConnectorsStoreNoCredentialMaterial
FAIL CDP-V14 synthetic provider inference and discovery use catalog facts
missing test: TestSyntheticCatalogProviderInferenceContract
FAIL CDP-V15 synthetic provider operator surfaces use catalog facts
missing test: TestSyntheticCatalogProviderOperatorSurfaces
FAIL CDP-V16 unsupported catalog declarations fail closed
missing test: TestUnsupportedCatalogPrimitivesFailClosed
FAIL CDP-V17 verified remote provider generations activate without rebuilds
missing test: TestVerifiedRemoteCatalogActivatesProvider
FAIL CDP-V18 runtime replacement is atomic and drains old connectors
missing test: TestRuntimeGenerationRejectsInvalidCandidates
FAIL CDP-V19 BYOK order and user-only noninterference are exact
missing test: TestBYOKStrategyOrderAndUserOnlyNoninterference
Summary: 0 passed, 19 failed
```

## CDP0 verification

- `bash -n scripts/verify-catalog-driven-providers.sh`: passed.
- `shellcheck scripts/verify-catalog-driven-providers.sh`: passed.
- Verifier roster: 19 named conditions.
- Red result: `Summary: 0 passed, 19 failed`.
- Production source changes: zero.
