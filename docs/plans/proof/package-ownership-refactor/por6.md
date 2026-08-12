# POR6 Starport authentication ownership proof

POR6 starts from clean Starport `main` at
`9bbeccefadce3c2afac41f6dc60135fba8b71e74`.

The task moves request-scoped inference authentication to
`internal/providers/auth`. It moves cloud credential acquisition to
`internal/credentials/cloudchain`. The task will preserve credential lifecycle,
redaction, request selection, and catalog-driven provider behavior.

## Fail-before evidence

The baseline contains 26 current source, test, script, and document matches for
the old path or package declaration. The directory contains request placement,
Google and Azure credential acquisition, shared cloud lifecycle code, and both
contract test groups.

## Focused implementation evidence

Request authentication now lives in `internal/providers/auth` with package name
`auth`. Google and Azure cloud chains now live in
`internal/credentials/cloudchain` with package name `cloudchain`. Callers use
the explicit aliases `providerauth` and `cloudchain` where those names clarify
the contract.

`TestProviderAuthenticationPackageHasNoCloudSDKImports` rejects Google, Azure,
and AWS SDK imports from request authentication.
`TestCloudChainPackageDoesNotMutateHTTPRequests` rejects `net/http`, request
authentication, and connector imports from cloud chains.

These checks pass with normal, uncapped Go scheduling:

```text
go test -count=1 ./internal/providers/auth ./internal/credentials/cloudchain ./internal/credentials ./internal/providers/connectors ./internal/config ./internal/diagnosis ./internal/app ./internal/architecture
go test -race -count=1 ./internal/providers/auth ./internal/credentials/cloudchain ./internal/credentials ./internal/providers/connectors ./internal/config ./internal/diagnosis ./internal/app ./internal/architecture
bash scripts/verify-package-layout.sh
bash scripts/test-package-layout-verifier.sh
bash scripts/verify-developer-experience.sh
bash scripts/verify-starmap-ownership.sh
bash scripts/verify-v1-architecture.sh
bash scripts/verify-doc-links.sh
technical-writing lint AGENTS.md DEVELOPMENT.md GLOSSARY.md docs/ARCHITECTURE.md docs/CONTRIBUTING.md docs/TASKS.md internal/providers/connectors/README.md --format text
```

The developer-experience verifier reports 47 passed and 0 failed. Architecture
and Starmap ownership each report 12 passed and 0 failed. Strict writing reports
zero diagnostics in all seven touched durable documents. The campaign verifier
passes POR-V01 through POR-V07 and reports 7 passed and 2 failed.
