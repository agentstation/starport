# CP3 — widen ModelInfo and populate ProviderInfo

Date: 2026-08-21. Branch: `codex/cp-3-widened-projections` (work authored on
top of the merged CP2 seam).

## Scope

Widen the `internal/catalog/view` projections with definition-level and
offering-level Starmap facts, and carry them through the OpenRouter codec,
the controllers, and the console API types.

- `ModelInfo` gains `authors`, `tags`, `lineage`, `knowledge_cutoff`,
  `open_weights`, and a per-provider `offerings` table with limits,
  availability, lifecycle, and all five token price dimensions.
- `ProviderInfo` gains `headquarters` and a `policies` block
  (privacy-policy URL, terms URL, retains-data, trains-on-data, retention
  details, moderated).
- `internal/protocol/openrouter` mirrors the new shapes with its own
  structs; it still never imports `internal/catalog/view`.
- `console/src/lib/api.ts` gains the matching TypeScript types.

## Deferred to CP6

Starmap v0.6.0 `Provider` has no description or docs-URL field. The CP3
sub-items "description" and "docs URL" need the Starmap v0.7.0 release that
CP6 owns. `CPV04` therefore stays red by design; the verifier condition was
tightened to `provider.Description` in `providers.go` so it cannot go green
before the field exists.

## Fail-before evidence

Written before the implementation, all three failed to compile or assert:

- `TestModelsCarryEveryOffering` (view seam, google-ai-studio +
  google-vertex fixture, asserts one model exposes offerings from ≥2
  distinct providers)
- `TestModelsCarryDefinitionFacts` (view seam, authors + offerings present)
- `TestProvidersCarryPolicyFacts` (view seam, headquarters + policies)
- `TestOpenRouterModelListCarriesEveryOffering` (controller + codec,
  two-offering fixture survives serialization with `cache_read` intact)

All four pass after the implementation.

## Gates

- `go test ./...` — ok, zero failures
- `go vet ./...` — clean
- `make lint` — 0 issues
- `make build` — ok
- `verify-starmap-ownership.sh` — 12 passed, 0 failed
- `verify-v1-architecture.sh` — 12 passed, 0 failed
- `verify-dependency-direction.sh` — 6 passed, 0 failed
- `verify-package-layout.sh` — passed
- `verify-openrouter-parity.sh` — 16 passed, 0 failed
- `verify-catalog-performance.sh` — 5 passed, 13 failed (CPV02 and CPV03
  newly green; CPV04 red by design until CP6; the rest belong to later
  tasks)
- Golden projection files regenerated with `-update-projection-golden`;
  the diff is purely additive (every removed line is a `]` that became
  `],`).

## Live acceptance evidence

Ephemeral dev gateway built from this change (`starport dev`, loopback,
in-memory, ephemeral one-time key, since shut down). `GET /api/v1/models`
returned 422 models, every one carrying an `offerings` table; 61 models
carried offerings from two or more providers. Sample (trimmed):

```json
{
  "id": "anthropic/claude-fable-5",
  "authors": [{ "id": "anthropic", "name": "Anthropic" }],
  "lineage": { "family": "claude-fable" },
  "offerings": [
    {
      "provider": "anthropic",
      "provider_model_id": "claude-fable-5",
      "context_length": 1000000,
      "pricing": {
        "prompt": "1e-05", "completion": "5e-05",
        "cache_read": "1e-06", "cache_write": "1.25e-05",
        "currency": "USD"
      }
    },
    {
      "provider": "deepinfra",
      "provider_model_id": "anthropic/claude-fable-5",
      "context_length": 1000000,
      "pricing": { "prompt": "1e-05", "completion": "5e-05", "currency": "USD" }
    }
  ]
}
```

`GET /api/v1/providers` sample (trimmed):

```json
{
  "id": "groq",
  "headquarters": "Mountain View, CA, USA",
  "policies": {
    "privacy_policy_url": "https://groq.com/privacy-policy/",
    "terms_of_service_url": "https://groq.com/terms-of-use/",
    "retains_data": false,
    "trains_on_data": false,
    "retention": "Input prompts and context are not retained; data is processed for immediate response generation and then discarded",
    "moderated": true
  }
}
```

This satisfies the CP3 acceptance criterion: `/api/v1/models` returns every
offering for a model served by two providers, with authors and policy facts
populated from the catalog snapshot.
