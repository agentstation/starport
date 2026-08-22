## Headline

Starmap already holds nearly everything needed for author pages, provider cards, and richer model cards — and it is all reachable from Starport **today, at the pinned v0.6.0, with no Starmap release required**. The `catalogs.Reader` interface exposes `Authors()`, `Author(id)`, `AuthorModels(id)`, `Definitions()`, and `DefinitionOfferings(id)`, and Starport's `RoutableSnapshot` already retains the whole `*catalogs.Catalog` in memory. It simply never reads any of it.

There is exactly **one** genuine Starmap gap: the high-quality logo SVGs exist in the repo and are compiled into the binary, but they sit behind `internal/` and are not carried in the catalog payload, so no external consumer can reach the bytes.

---

## 1. Author data — rich, complete, and unused

**Type:** `/Users/jack/src/github.com/agentstation/starmap/pkg/catalogs/author.go:10`

```go
type Author struct {
	ID           AuthorID   `json:"id" yaml:"id"`
	Aliases      []AuthorID `json:"aliases,omitempty"`
	Name         string     `json:"name"`
	Description  *string    `json:"description,omitempty"`
	Headquarters *string    `json:"headquarters,omitempty"`
	IconURL      *string    `json:"icon_url,omitempty"`
	Website      *string    `json:"website,omitempty"`
	HuggingFace  *string    `json:"huggingface,omitempty"`
	GitHub       *string    `json:"github,omitempty"`
	Twitter      *string    `json:"twitter,omitempty"`
	Catalog      *AuthorCatalog `json:"catalog,omitempty"`
	CreatedAt    utc.Time   `json:"created_at"`
	UpdatedAt    utc.Time   `json:"updated_at"`
}
```

**Data file:** `/Users/jack/src/github.com/agentstation/starmap/internal/embedded/catalog/authors.yaml` — 104 authors. Field coverage: `website` 87, `icon_url` 83, `huggingface` 71, `github` 68, `description` 49, `twitter` 37, `headquarters` 6. Headquarters is the one thin field.

**How models link to authors — three independent paths, all already populated:**

1. **The model ID itself encodes the author.** `ModelDefinitionID` is literally `author/slug` (`authored_model.go:80`, `AuthoredModelID` returns `string(authorID) + "/" + slug`). Every ID Starport already serves on `/api/v1/models` — `anthropic/claude-opus-4-5-20251101` — carries its author in the string. `ParseModelDefinitionID` splits it back out with no catalog lookup.
2. **`ModelDefinition.AuthorIDs []AuthorID`** (`model_definition.go:17`) — a list, supporting co-authored models.
3. **Reverse index:** `Catalog.AuthorModels(authorID) ([]ModelDefinition, error)` at `readonly.go:326`. An author page is one call.

Attribution is derived, not hand-maintained: `AuthorAttribution` (`author.go:61`) carries glob patterns (`qwen-*`, `*-qwen-*`) plus an optional `provider_id`, and `author_views.go:37` resolves them at catalog build time. Aliases resolve through `Authors().Resolve()`.

**Storage layout:** `internal/embedded/catalog/authors/<author-id>/models/<slug>.yaml` — 591 authored model files across 44 author directories.

---

## 2. Logos — the one real gap

Two distinct things exist, and only the worse one is reachable.

**Reachable today (via the Go struct):** `Author.IconURL` and `Provider.IconURL`. But inspect the actual values — they are hotlinked third-party favicons: `https://www.anthropic.com/favicon.ico`, `https://01.ai/favicon.ico`, `https://alibabacloud.com/favicon.ico`. For a console these are poor: 16–32px, wrong on dark backgrounds, cross-origin, and they break whenever a vendor reshuffles their site.

**Not reachable:** 26 real SVG logos committed in the repo and compiled into the binary.

```
internal/embedded/catalog/authors/{alibaba,anthropic,deepseek,google,groq,huggingfaceh4,
  meta,mistral,moonshot-ai,nvidia,openai,perplexity,together,xai,zhipu-ai}/logo.svg   (15)
internal/embedded/catalog/providers/{alibaba,anthropic,cerebras,deepinfra,deepseek,
  fireworks-ai,google-ai-studio,google-vertex,groq,moonshot-ai,openai}/logo.svg       (11)
```

Total 108 KB. They are covered by `//go:embed catalog sources` in `internal/embedded/embed.go:9`, sourced from models.dev by `internal/sources/modelsdev/merge.go` (`CopyProviderLogos` / `CopyAuthorLogos`, with alias fallback), and preserved by the filesystem catalog writer (`pkg/catalogs/catalog_fs_test.go:315` asserts this).

Why Starport can't get them:

- `internal/embedded` is an internal package — not importable from Starport.
- `CatalogPayload` (`pkg/catalogs/payload.go:16`) carries `Providers []Provider`, `Authors []Author`, `ProviderModels`, `AuthorModels`, `Provenance`. **No binary assets.** So logos don't travel through the generation store or the released `starmap-catalog.tar.gz` artifact either — and Starport's `internal/catalog/runtime.go:47` builds its client from `WithCatalogStore(generationStore)`, i.e. the payload path.
- There is no `Logo` field on `Author` or `Provider`, and no accessor anywhere in `pkg/`.

**Coverage is also partial:** 15 of 104 authors (14%) and 11 of 15 providers (73%; `hetzner` has none) have an SVG.

---

## 3. Provider metadata beyond id+name

`/Users/jack/src/github.com/agentstation/starmap/pkg/catalogs/provider.go:14`. Populated counts across the 15 providers in `providers.yaml`:

| Field | Type | Populated |
|---|---|---|
| `aliases` | `[]ProviderID` | 6 |
| `headquarters` | `*string` | 13 |
| `icon_url` | `*string` | 14 |
| `status_page_url` | `*string` | 11 |
| `catalog.docs` | `*string` | 14 |
| `privacy_policy` | `*ProviderPrivacyPolicy` | 8 |
| `retention_policy` | `*ProviderRetentionPolicy` | 8 |
| `governance_policy` | `*ProviderGovernancePolicy` | 8 |
| `extensions` | `SourceExtensions` | 11 |

The policy sub-structs are the interesting ones for a provider card, and they map directly onto what OpenRouter shows:

```go
type ProviderPrivacyPolicy struct {
	PrivacyPolicyURL  *string  // privacy_policy_url
	TermsOfServiceURL *string  // terms_of_service_url
	RetainsData       *bool    // retains_data
	TrainsOnData      *bool    // trains_on_data
}
type ProviderRetentionPolicy struct {
	Type     ProviderRetentionType  // enum
	Duration *time.Duration         // nil = forever, 0 = immediate
	Details  *string
}
type ProviderGovernancePolicy struct {
	ModerationRequired *bool
	Moderated          *bool
	Moderator          *string
}
```

Also present: `ProviderInference.HealthAPIURL` and `HealthComponents` (`provider.go:154`), which would drive a live status indicator.

---

## 4. Model metadata Starport's console does not surface

From `ModelDefinition` (`model_definition.go:14`) and its sub-structs, with real-data coverage over the 591 authored model files:

- `Metadata.ReleaseDate` — 406 files
- `Metadata.KnowledgeCutoff` — 146 files
- `Metadata.Tags []ModelTag` — 123 files. The vocabulary (`model_tags.go`) has ~30 values: `coding`, `reasoning`, `vision`, `multimodal`, `embedding`, `text_to_image`, `medical`, `legal`, `finance`, etc. This is a ready-made faceted filter.
- `Description` — 396 files
- `Lineage` — `Family` (e.g. `claude-opus`), `Root`, `Parent`. Entirely dropped; this is the data for a model-family grouping or a lineage view.
- `Weights.Open *bool` — the open-weights badge.
- `Capabilities` — seven sub-structs dropped whole: `Attachments`, `Generation`, `Reasoning` (`ModelControlLevels`: `levels: [low, medium, high]`, `default`), `ReasoningTokens` (`{min: 1024, max, default}`), `Verbosity`, `Tools` (`ToolChoices`, `WebSearch` config), `Delivery`.
- `CreatedAt` / `UpdatedAt`.

Per-offering (`ProviderOffering`, `provider_offering.go:129`), Starport reads only the *first* route's offering, so per-provider variance is invisible. Dropped: `Regions`, `Modes`, `Availability` (`available` / `restricted` / `unavailable` / `unknown`), `Lifecycle` (`active` / `unknown` / …), `Limits.InputTokens`, and `Pricing.Tokens.Reasoning` / `.CacheRead` / `.CacheWrite`.

Note on deprecation dates specifically: **Starmap has lifecycle *state* (`OfferingLifecycle` enum) but no retirement/sunset *date* field.** That is a real schema gap if you want "retires on 2026-10-01" on a model card.

---

## 5. Starport's projection gap

`/Users/jack/src/github.com/agentstation/starport/internal/catalog` defines **no presentation types at all** — only a routing projection. `Route` (`snapshot.go:12`) keeps `DefinitionID`, `ProviderID`, `ProviderModelID`, `Operations`, `Endpoints`, `PromptCache`. The console-facing JSON is assembled in `internal/proxy/service.go:122-215` (`ModelInfo`, `ProviderInfo`) and re-projected once more by the protocol codecs.

The findings that matter:

- **Authors: one line of contact.** `internal/proxy/proxy.go:498-499` does `ownedBy = string(definition.AuthorIDs[0])` and that string goes out as `owned_by` on `/v1/models` only. `/api/v1/models` drops it. The console's TypeScript `Model` type (`console/src/lib/api.ts:167`) does not even declare it. `Catalog.Author()` and `Catalog.Authors()` are never called anywhere in Starport.
- **Logos: zero contact.** No Go source in Starport contains `icon_url`, `IconURL`, or `logo`. Every `icon` hit is lucide-react nav chrome or a favicon `<link>`.
- **`ProviderInfo.Description` and `AuthDescription` are declared but never populated.** `ProviderInfo.URL` is filled from `StatusPageURL`, not a homepage.
- **The design already specifies what's missing.** `DESIGN.md:262` — "Row: provider icon · display name · capability badge icons"; `DESIGN.md:246` — "provider icon · short". Designed, never built, no data path.
- **The SPA models table has four columns** (`console/src/components/models/ModelsTable.tsx`): model, capabilities, context, price/1M. No author, no provider icon, no description, no release date.
- `Catalog.Provenance()` is never called — the "where did this fact come from" audit trail is fully available and fully unused.

---

## 6. Versioning and release

**Two independent tracks.**

*Go module (schema changes):* tag `v*` → `.github/workflows/release.yaml`. Requires the tag be an ancestor of `origin/main`, runs `make embedded-catalog-budget-check`, `make verify`, `make release-check`, then goreleaser with build attestation. Locally: `make release VERSION=x.y.z` (clean → fix → check → tag → push) or just `make release-tag VERSION=x.y.z`. Latest tag is **v0.6.0**.

*Catalog data (no Go release needed):* `.github/workflows/catalog-generation.yaml`, nightly at 03:17 UTC. Refreshes from live provider APIs using per-provider secrets, computes a semantic checksum via `CatalogSemanticChecksum` (payload minus provenance), and if the semantics changed publishes a **prerelease tagged `catalog-semantic-<sha256-digest>`** carrying an attested `starmap-catalog.tar.gz`. It verifies against existing releases before republishing and picks a compatible previous tag for diffing. Budget gate: review thresholds at 16 MB uncompressed / 8 MB compressed (`internal/bootstrap/budget/budget.go:140`) — 108 KB of logos is noise against that.

*Starport's pin:* `/Users/jack/src/github.com/agentstation/starport/go.mod:13` — `github.com/agentstation/starmap v0.6.0`, no `replace` directives.

I diffed the module cache at `v0.6.0` against Starmap HEAD: **`Author` and `Provider` are byte-identical**, and the v0.6.0 embedded catalog already ships 104 authors, 15 providers, and all 26 logo SVGs. So author cards, provider policy cards, and richer model cards need **no Starmap release at all**.

---

## Smallest set of Starmap additions

**For author pages and richer model cards: nothing.** All of it is Starport-side projection work against the pinned v0.6.0. Add `Author` fields to a new `AuthorInfo` DTO in `internal/proxy`, read via `snapshot.Catalog().Authors()`, serve at `/api/v1/authors` and `/api/v1/authors/{id}` (backed by `Catalog.AuthorModels(id)`), and widen `ModelInfo` to carry the lineage/tags/knowledge-cutoff/reasoning fields already sitting in `ModelDefinition`.

**For logos: one change, three options.**

The one worth doing is to carry the bytes in the payload so they travel through the existing generation-store and released-artifact paths that Starport already consumes:

1. Add `Logo *CatalogAsset` (or `LogoSVG []byte`) to `Author` and `Provider`, populated by the FS loader from `<kind>/<id>/logo.svg`, and add the field to `CatalogPayload`. Cost: a `SchemaVersion` bump plus a v0.7.0 module release; +108 KB uncompressed, far under budget. This is the only option that survives Starport's remote-catalog path.
2. Cheaper but weaker: re-export the embedded FS as `pkg/catalogs.EmbeddedAssets() fs.FS`. Still a v0.7.0 release, and it only works for the embedded-bootstrap path — a Starport instance running off a remote generation store gets nothing.
3. Cheapest and worst: use `IconURL` as-is. Zero Starmap work, but you ship hotlinked 16px vendor favicons.

**Two data-coverage gaps worth filling regardless of schema:** author logo coverage is 15/104 (the models.dev merge only finds a logo when an author ID matches a models.dev *provider* ID, so pure model labs like `black-forest-labs` and `nousresearch` get nothing), and `headquarters` is populated for only 6 of 104 authors.

**One genuine schema gap** if the model card needs sunset dates: `OfferingLifecycle` is a state enum with no accompanying `deprecated_at` / `retires_at` timestamps. Adding those to `ProviderOffering` would be the second item in a v0.7.0.