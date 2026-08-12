# POR3 Starmap provider-fixture proof

POR3 starts from clean Starmap `main` at
`13779dcffe99cbabc6e8b6257c4ff20796eeeb59`.

## Fail-before evidence

Four test-only packages duplicate facts that embedded provider YAML already
owns:

- `internal/providers/cerebras`
- `internal/providers/deepseek`
- `internal/providers/groq`
- `internal/providers/moonshot-ai`

Their tests construct provider IDs, names, API-key environment names, header
placements, schemes, catalog endpoint URLs, field mappings, and author mappings
in Go. The embedded records already supply these facts. The OpenAI-compatible
transport is the stable compiled primitive.

Each raw capture lives below its provider-named package. The fixture helper
infers identity from that layout instead of accepting an explicit provider.
The refresh script runs each provider package with `-update`, but no test calls
the write helper. A successful test command therefore produces no update and
the script correctly returns nonzero. The advertised successful refresh path
does not exist.

Current architecture and testing text also gives two different instructions:
write live raw payloads under `/tmp`, and update governed checked-in fixtures.
POR3 must distinguish exploratory shape capture from an explicit reviewed
fixture refresh.

## Initial verifier

POR-V04 is red because the shared catalog contract and refresh harness do not
exist. The complete campaign reports 3 passed and 6 failed.

## Focused implementation evidence

The implementation moves OpenAI, Cerebras, DeepSeek, Groq, and Moonshot AI
captures below `internal/providers/openai/testdata/providers/{provider}`. One
discovered transport contract loads the real embedded provider record. It
proves endpoint type and URL, catalog credential metadata, every configured
field mapping, every configured author mapping, and exact opaque response IDs.

The shared fixture package now accepts explicit provider identity. It proves
deterministic discovery, metadata integrity, freshness, tamper rejection,
canonical capture, and temporary-file cleanup. The opt-in refresh command uses
the catalog-driven acquisition composition. Its shell contract proves help,
input validation, missing providers, fetch failure, no-op failure, successful
update, and sibling isolation.

These focused gates pass:

```text
go test -count=1 ./internal/providers/openai ./internal/test/providerfixture -run '^(TestOpenAICompatibleProviderCatalogContracts|TestProviderFixture)'
go test -race -count=1 ./internal/providers/openai ./internal/test/providerfixture -run '^(TestOpenAICompatibleProviderCatalogContracts|TestProviderFixture)'
bash scripts/test-provider-testdata-refresh.sh
bash scripts/verify-package-layout.sh
go test -count=1 ./internal/providers/... ./internal/embedded
go test -race -count=1 ./internal/providers/... ./internal/embedded
make docs-check
```

The campaign verifier now passes POR-V01 through POR-V04 and reports 4 passed
and 5 failed. POR-V05 through POR-V09 remain red because their owning tasks have
not run.

## Complete local verification

The final-state `make verify` run passes without a scheduler cap. It includes
ordinary tests, pure-Go consumer tests, package-layout checks, the full race
suite, vet, lint, coverage, generated documentation, build, catalog validation,
and CLI smoke checks. The root race package passed in 492.711 seconds. Catalog
validation passed for 14 providers, 104 authors, and 611 models. The catalog
benchmark passed three times at 8.744, 8.966, and 9.061 ns/op with zero bytes
and zero allocations per operation.

SHA-256 comparison proves that each of the five payloads and five metadata
files is byte-identical before and after its move. `git diff --check` is clean.
The final campaign run repeats 4 passed and 5 failed.
