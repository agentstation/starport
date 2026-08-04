# SVA9 protocol and public-boundary proof

Date: 2026-08-03
Status: done

## Fail-before

The OpenAI and OpenRouter routes decoded the same server DTOs and encoded the
same response and error values. Middleware selected one error shape for both
protocols. Streaming failures had no OpenRouter error chunk contract. The
server also selected protocol behavior from URL prefixes inside shared
controllers.

Several layers owned overlapping inference values. The values appeared in
provider wire types, proxy types, cache records, and server DTOs.

Unused public packages also repeated these values. `pkg/providers` defined a
second connector contract with no production consumer. `pkg/catalog` held a
second model catalog. The router and connector metadata also held static model
capabilities, prices, offerings, and provider facts outside Starmap.

Provider IDs still accepted unreleased aliases. The old `providerkey:` and
`provider_key:` namespaces were already identified by SVA1. Google and Azure
adapter aliases added more paths that no released client needed.

The repository had no repeatable SDK smoke harness. Therefore, no executable
result supported an SDK compatibility claim.

## External contract research

The implementation review used the current official OpenRouter pages for the
API overview, quickstart, model discovery, and SDK entry points:

- <https://openrouter.ai/docs/api/reference/overview>
- <https://openrouter.ai/docs/quickstart>
- <https://openrouter.ai/models>
- <https://openrouter.ai/docs/client-sdks/overview>
- <https://openrouter.ai/docs/client-sdks/python/overview>
- <https://openrouter.ai/docs/client-sdks/typescript/overview>
- <https://openrouter.ai/docs/client-sdks/go/overview>

The smoke harness uses the documented base-URL and bearer-key substitution.
It checks chat, streaming, model discovery, and embeddings through
`/api/v1`. The optional SDK runners target the current official Python,
TypeScript, and Go packages.

## Change

`internal/httpapi/openai` and `internal/httpapi/openrouter` now own separate
wire DTOs and codecs. Each codec converts once to or from `internal/inference`.
OpenAI and OpenRouter routes receive separate controllers. Controllers do not
inspect URL prefixes. Middleware uses route-scoped protocol context to encode
authentication and rate-limit errors in the selected dialect.

The OpenRouter codec owns numeric error codes, provider details, routing
fields, enhanced model-list fields, and streaming error chunks. The OpenAI
codec owns the OpenAI request, result, model-list, and error shapes. Protocol
DTO repetition is intentional. Business semantics are not repeated.

The proxy now carries canonical inference values plus gateway policy and
metadata. The old request DTO and shared failure DTO are gone. The configured
request-size limit now applies to both protocol groups.

The unused `pkg/catalog`, `pkg/models`, `pkg/providers`, and `pkg/httpclient`
packages are gone. The HTTP client implementation moved behind
`internal/httpclient`. The public-boundary fitness test rejects a repository
`pkg` directory, Starport public-package imports, and protocol imports outside
the approved adapter seams. The removed `.DS_Store` remains recoverable at
`/tmp/starport-pkg-DS_Store-sva9`.

Starmap is now the only owner of model capabilities, limits, prices,
offerings, and provider facts. The router's static capability and price maps
are gone. `openrouter/auto` asks the pure planner to consider the current
routable snapshot. Explicit models can precede that catalog-wide fallback
without making catalog order a policy.

Cache prices and provider discovery also use the active Starmap generation.
Connectors with live model-discovery APIs report live observations or an
error. They do not substitute static lists. Vertex returns an unsupported
error instead of inventing a list.

The unreleased Google and Azure adapter aliases are gone. Provider policy now
uses exact adapter IDs. Starmap resolves canonical model definitions to exact
provider offerings. No migration or compatibility branch remains in the
runtime path.

## Contract evidence

`TestOpenAIProtocolContract` and `TestOpenRouterProtocolContract` cover decode,
result encode, streaming, models, and error dialects.
`TestProtocolMiddlewareSelectsErrorDialect` proves that route middleware uses
the correct error contract. Controller tests prove the separate route
controllers.

`TestPublicPackageBoundary` proves the binary-first package boundary and the
protocol import graph. `TestRouterUsesRoutableSnapshot` proves snapshot-backed
exact, automatic, and mixed explicit/automatic routing. The route planner
contract proves that an explicit model stays ahead of the any-model fallback.
Registry and proxy tests prove that provider metadata and cache-token prices
come from Starmap and fail closed without it.

## Verification

These commands passed:

```bash
go test ./internal/httpapi/... ./internal/server ./internal/architecture
go test ./internal/catalog ./internal/router ./internal/routing ./internal/proxy
go test ./internal/providers/connectors ./internal/registry
go test ./...
bash scripts/verify-v1-architecture.sh
bash scripts/smoke-openrouter-sdks.sh
git diff --check
```

The smoke result is:

```text
PASS raw HTTP chat
PASS raw HTTP stream
PASS raw HTTP models
PASS raw HTTP embeddings
UNVERIFIED Python OpenRouter SDK: package 'openrouter' is not installed
UNVERIFIED TypeScript OpenRouter SDK: package '@openrouter/sdk' is not installed
UNVERIFIED Go OpenRouter SDK: package is not part of this module
```

The SDK states are optional external checks. They are not reported as green.
The verifier reports:

```text
PASS V01 Starmap module and Go floor
PASS V02 canonical inference contract
PASS V03 routable snapshot generation contract
PASS V04 deterministic route planner contract
PASS V05 attempt state and retry budget contract
PASS V06 versioned concept repository contracts
PASS V07 response cache semantic identity contract
PASS V08 production composition fail-closed contract
PASS V09 public package boundary contract
PASS V10 OpenRouter protocol contract
PASS V11 import graph architecture fitness
PASS V12 full Go test suite
Summary: 12 passed, 0 failed
```
