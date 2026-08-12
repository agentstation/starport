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
