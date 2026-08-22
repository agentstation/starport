Findings.

## 1. `internal/catalog` — the projection layer

Files (non-test): `control_plane.go`, `snapshot.go`, `runtime.go`, `remote_runtime.go`, `freshness.go`, `generation_store.go`, `generation_index.go`, `identity.go`.

Key insight: **`internal/catalog` does not define model/provider *presentation* types at all.** It defines a routing projection only. The console-facing model/provider JSON is built in `internal/proxy`.

`/Users/jack/src/github.com/agentstation/starport/internal/catalog/snapshot.go:12` — the only per-model type in the package:

```go
type Route struct {
	CatalogGenerationID string
	DefinitionID        catalogs.ModelDefinitionID
	ProviderID          catalogs.ProviderID
	ProviderModelID     catalogs.ProviderModelID
	Operations          []catalogs.ProviderOperation
	Endpoints           []catalogs.ProviderOfferingEndpoint
	PromptCache         *bool
}
```

No JSON tags — it never serializes. `RoutableSnapshot` (`snapshot.go:63`) retains the whole immutable `*catalogs.Catalog` plus generation identity, so **the full Starmap catalog is in memory and reachable** via `snapshot.Catalog()`; the projections simply do not read most of it. `deriveRoutableSnapshot` (`control_plane.go:310`) is the only place offerings are walked, and it copies exactly: DefinitionID, ProviderID, ProviderModelID, adapter-compatible Operations/Endpoints, and `Service.PromptCache`. Everything else on `ProviderOffering` (Pricing, Limits, Availability, Regions, Lifecycle, Modes) is read only for filtering, not retained on `Route`.

The only JSON-tagged structs in the package are freshness/ops types in `freshness.go`: `SnapshotMetadata`, `ValidationSummary`, `SourceObservation`, `OfferingChange`, `PriceChange`, `Diff`, `RefreshReport` — plus `GenerationIndexEntry` in `generation_index.go:26`. These serve `/api/v1/catalog` and `/api/v1/catalog/changes`, not models/providers.

## 2. HTTP handlers

Routes: `/Users/jack/src/github.com/agentstation/starport/internal/server/routes.go:38,58,63`

- `GET /v1/models` → `controllers.Models.List` (OpenAI protocol)
- `GET /api/v1/models`, `/{model}`, `/{model}/endpoints` → `controllers.OpenRouterModels` (OpenRouter protocol)
- `GET /api/v1/providers` → `controllers.Providers.List`

Handlers: `/Users/jack/src/github.com/agentstation/starport/internal/server/controllers/models.go`, `.../providers.go`. Both delegate to `proxy.Proxy` (`internal/proxy/proxy.go:468 modelsResponseFromSnapshot`, `:641 ListProviders`).

Internal DTOs (`/Users/jack/src/github.com/agentstation/starport/internal/proxy/service.go:122-215`):

```go
type ModelInfo struct {
	ID            string `json:"id"`
	CanonicalSlug string `json:"canonical_slug,omitempty"`
	Name          string `json:"name,omitempty"`
	Object        string `json:"object"`
	Created       int64  `json:"created"`
	OwnedBy       string `json:"owned_by"`

	Pricing             *ModelPricing      `json:"pricing,omitempty"`
	Context             *int               `json:"context_length,omitempty"`
	Type                string             `json:"type,omitempty"`
	Description         string             `json:"description,omitempty"`
	Architecture        *ModelArchitecture `json:"architecture,omitempty"`
	TopProvider         *TopProviderInfo   `json:"top_provider,omitempty"`
	SupportedParameters []string           `json:"supported_parameters,omitempty"`
}

type ProviderInfo struct {
	ID               string                `json:"id"`
	Name             string                `json:"name"`
	Description      string                `json:"description,omitempty"`
	URL              string                `json:"url,omitempty"`
	Models           []string              `json:"models"`
	Capabilities     []string              `json:"capabilities,omitempty"`
	RequiresAuth     bool                  `json:"requires_auth"`
	AuthDescription  string                `json:"auth_description,omitempty"`
	CredentialFields []CredentialFieldInfo `json:"credential_fields,omitempty"`
}
```

Note `ProviderInfo.Description` and `AuthDescription` are **declared but never populated** — `providerInfosFromRuntime` sets only ID, Name, RequiresAuth, CredentialFields, URL (from `StatusPageURL`, not a homepage), Models, Capabilities.

Wire shape is re-projected once more before writing:

- `/v1/models` → `openai.ModelList{object, data[]}` where `openai.Model` is only `{id, object, created, owned_by}` (`internal/protocol/openai/codec.go:364`). Everything enriched is **dropped on this surface**.
- `/api/v1/models` → `openrouter.ModelList{data[], total_count, links{next}}` with `openrouter.Model{id, canonical_slug, name, created, description, context_length, architecture, pricing, top_provider, supported_parameters}` (`internal/protocol/openrouter/codec.go:456`). Note `ModelInfo.Type` and `Pricing.Currency` are dropped here — `openrouter.Pricing` is only `{prompt, completion}`.
- `/api/v1/providers` → `ProvidersResponse{providers: [...]}` serialized directly via `dto.WriteJSON`.

## 3. Author / authors — every hit

Non-test, non-`Authorization`:

- `/Users/jack/src/github.com/agentstation/starport/internal/proxy/proxy.go:498` — `if len(definition.AuthorIDs) > 0 {`
- `/Users/jack/src/github.com/agentstation/starport/internal/proxy/proxy.go:499` — `ownedBy = string(definition.AuthorIDs[0])`
- `/Users/jack/src/github.com/agentstation/starport/docs/CONTRIBUTING.md:144` — prose only

Test-only (fixture construction, never assertions on projection):
- `internal/catalog/starmap_fact_mutation_test.go:164,174,176`
- `internal/catalog/runtime_contract_test.go:103,104,106`
- `internal/catalog/freshness_test.go:43,50,53`
- `internal/router/embeddings_test.go:94,100,101`
- `internal/router/endpoint_binding_test.go:144,149,150`

**Conclusion:** Starport touches Starmap authorship in exactly one place — it flattens `AuthorIDs[0]` into the raw string `owned_by`. It never calls `catalog.Author(id)` or `catalog.Authors()`, so it never obtains `Author.Name`, `Description`, `IconURL`, `Website`, `GitHub`, `HuggingFace`, `Twitter`, or `Headquarters`. The console TS `Model` type (`console/src/lib/api.ts:167`) does not even declare `owned_by`.

## 4. icon_url / IconURL / logo / icon / branding — every hit

**Zero hits for `icon_url`, `IconURL`, `logo`, or `branding` in any Go source.** Starmap's `Provider.IconURL` and `Author.IconURL` are never read.

Every `icon` hit is local UI chrome, unrelated to catalog data:
- `internal/console/templates/index.html:8,80,84,90` — favicon link, theme-toggle and GitHub icon buttons
- `internal/console/static/css/console.css:4,138,139,175,188,189,190,704,733,764` and `internal/console/static/css/chat.css:62,63,184,185,518` — `.icon-btn` styling
- `internal/console/handler_test.go:218` — favicon MIME assertion
- `console/index.html:6` — favicon link
- `console/src/components/shell/Shell.tsx:23-30,80,88,145,154,176` — lucide nav icons
- `console/src/components/ui/CopyButton.tsx:28,39`, `console/src/routes/settings.tsx:157-188` — lucide icons
- `DESIGN.md:147,192,206,246,262,291` and `docs/plans/…` — design prose. `DESIGN.md:262` explicitly specifies "Row: provider icon · display name · capability badge icons" and `DESIGN.md:246` "provider icon · short" for the model picker — **designed for, never implemented, and no data path exists to feed it.**

## 5. Starmap pin

`/Users/jack/src/github.com/agentstation/starport/go.mod:13`:

```
	github.com/agentstation/starmap v0.6.0
```

**No `replace` directives anywhere in go.mod.** Module cache resolves to `/Users/jack/go/pkg/mod/github.com/agentstation/starmap@v0.6.0`.

## 6. Console / web UI

Two consoles, selected by `Console.Next` at `/Users/jack/src/github.com/agentstation/starport/internal/app/app.go:393-412`:

- **Legacy** (default): `internal/console/handler.go` — Go `html/template` shell (`templates/index.html`) plus embedded `static/js/{api,app,router,ui,freshness,markdown}.js` and `static/css`. `internal/console/static/js/api.js:78,89,93` fetch `/api/v1/models`, `/api/v1/models/{id}/endpoints`, `/api/v1/providers`.
- **SPA** (`Console.Next`): `internal/console/spa.go` — `//go:embed all:dist`, serves the Vite build of `/Users/jack/src/github.com/agentstation/starport/console` (React + TanStack Router/Query).

SPA rendering:
- `console/src/routes/models.tsx` + `console/src/components/models/ModelsTable.tsx` — four columns only: **model** (`id` mono + `name` if different), **capabilities** (badges derived from `architecture.input_modalities` and `supported_parameters`), **context** (`context_length`), **price / 1M** (`pricing.prompt`/`completion`). No author, no provider icon, no description, no release date, no lineage.
- `console/src/routes/providers.tsx` — cards keyed on `/api/v1/admin/providers` runtime status (adapter dot, credential pill, offering counts), with `/api/v1/providers` (`ProviderCatalogEntry{id, name, url, models, credential_fields}`) used only for display name and a fallback catalog-only list. No logo, no description, no policy links.
- The console's `Model` TS type (`console/src/lib/api.ts:167-179`) is narrower than what the API returns — it omits `canonical_slug`, `owned_by`, `created`, `object`, `architecture.tokenizer`, and `top_provider.context_length`.

## Starmap fields available but not projected

**`catalogs.Author` — entirely unreachable.** Nothing calls `Catalog.Author()`/`Catalog.Authors()` (both exist on the immutable `*catalogs.Catalog`, `readonly.go:216,240`). Dropped in full: `Name`, `Aliases`, `Description`, `Headquarters`, `IconURL`, `Website`, `HuggingFace`, `GitHub`, `Twitter`, `Catalog`, `CreatedAt`, `UpdatedAt`. Only `AuthorIDs[0]` survives, as an opaque `owned_by` string on `/v1/models` (and not even on `/api/v1/models`).

**`catalogs.Provider`** (`provider.go:14`) — projected: `ID`, `Name`, `StatusPageURL`→`url`, `Credentials` (inference profile fields). Dropped: `Aliases`, `Headquarters`, **`IconURL`**, `Catalog`, `Inference`, `PrivacyPolicy`, `RetentionPolicy`, `GovernancePolicy`, `Extensions`.

**`catalogs.ModelDefinition`** (`model_definition.go:14`) — projected: `ID`(→`id`+`canonical_slug`), `Name`, `Description`, `AuthorIDs[0]`, `Metadata.ReleaseDate`/`CreatedAt`(→`created`), `Weights.Architecture.Tokenizer`, `Capabilities.Features.Modalities`, and 17 booleans off `Capabilities.Features`. Dropped:
- `UpdatedAt`
- `Metadata.KnowledgeCutoff`, `Metadata.Tags`
- `Lineage` entirely (`Family`, `Root`, `Parent`)
- `Weights.Open` (open-weights flag)
- `Weights.Architecture` beyond `Tokenizer` (everything else in `ModelArchitecture`)
- `Capabilities.Attachments`, `.Generation`, `.Reasoning`, `.ReasoningTokens`, `.Verbosity`, `.Tools`, `.Delivery` — all seven sub-structs
- Within `Capabilities.Features`: `ToolCalls`, `WebSearch`, `Attachments`, `ReasoningTokens`, `IncludeReasoning`, `Verbosity`, `TopA`, `MinP`, `TypicalP`, `TFS`, `StopTokenIDs`, `RepetitionPenalty`, `NoRepeatNgramSize`, `LengthPenalty`, `BadWords`, `AllowedTokens`, and the rest below line 207 of `model.go`

**`catalogs.ProviderOffering`** (`provider_offering.go:129`) — only the *first* route's offering is read for enrichment (`proxy.go:513-520`), so per-provider price/limit variance is invisible. Projected: `Limits.ContextWindow`, `Limits.OutputTokens`, `Pricing.Currency`, `Pricing.Tokens.Input`, `Pricing.Tokens.Output`. Dropped: `Regions`, `Modes`, `Lifecycle` and `Availability` (used for filtering only, never surfaced), all other `ModelLimits` fields, and `Pricing.Tokens.Reasoning` / `.CacheRead` / `.CacheWrite` — note these three *are* diffed in `/api/v1/catalog/changes` (`freshness.go:246`) but never exposed on the model itself.

**Generation provenance** — `Catalog.Provenance()` (`readonly.go:232`) is never called by any Starport code.