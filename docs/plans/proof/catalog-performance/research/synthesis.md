# Design review v2 — synthesis (draft)

Inputs: baseline-notes.md, competitor-research.md, catalog-projection-audit.md,
starmap-audit.md, exemplar-hunt.md. Directive: make Starport best in class —
catalog traversal (providers/models/authors), logos, composer retooling,
gateway-overhead performance identity, enterprise-trust polish.

## Decisions (proposed, grounded in research)

### D1. Catalog information architecture: the provider/author/model triangle
Adopt modelwiki's IA (it mirrors Starmap ownership):
- `/models` list -> `/models/$modelId` detail (flagship page, OpenRouter pattern)
- `/providers` grid -> `/providers/$id` detail
- `/authors` list (or facet) -> `/authors/$id` detail
Cross-links everywhere: model page has per-provider offering table + author
link; provider page has served-models list + health; author page has model
catalog + provider availability. Model detail's primary CTA: "Open in chat"
(Jan's "Use model" pattern) — doubles as routing preview.

### D2. New gateway API surface (internal/proxy + server/controllers)
- `GET /api/v1/authors`, `GET /api/v1/authors/{id}` (AuthorInfo DTO: name,
  description, website/github/hf/twitter, hq, model ids) — reads
  snapshot.Catalog().Authors()/AuthorModels(). No Starmap release needed.
- Widen ModelInfo: owned_by/authors on /api/v1/models, tags, lineage/family,
  knowledge cutoff, open-weights flag, reasoning controls, per-offering table
  (all offerings, not just first route: price incl. reasoning/cache-read/
  cache-write, context, availability, lifecycle).
- Populate ProviderInfo.Description; add policy trio (privacy/retention/
  governance), docs URL, headquarters, health/status link.
- Logos: `GET /console/assets/logos/{kind}/{id}.svg` (or /api/v1 route) —
  served from embedded/bundled set; console falls back to initials.

### D3. Logo strategy (air-gapped)
- Starmap v0.7.0: carry logo bytes in CatalogPayload (Author.Logo,
  Provider.Logo) — option 1 from starmap-audit; survives remote-catalog path.
  +108KB, far under budget. Also fill author-logo coverage gaps (15/104) using
  lobe-icons/simple-icons (MIT/CC0) harvest where licensing is clean.
- Starport: serve logos from the snapshot; console renders with agentgateway's
  mono/color split + initials fallback (two-letter bordered circle).
- Until v0.7.0 lands: bundle an interim SVG set in console assets keyed by
  provider/author id (same fallback chain), then switch source to catalog.

### D4. Providers page revamp (user's headline complaint)
- Compact card: logo + display name + slug inline on ONE row (slug as muted
  mono suffix, not its own chip row); single status treatment (one badge);
  "N models · M available" phrasing; click -> provider detail.
- Provider detail: identity (logo, name, hq, links, policy trio), credential
  state + CTA to API keys, served models table, health: availability state,
  circuit state, rolling latency/TTFT/throughput/error-rate — the chart
  OpenRouter DOESN'T have on provider pages (our edge).
- Page-level search/sort (name, status, model count).

### D5. Model detail page (flagship)
Header: name + author chip (link), id (copy), context, created/release,
knowledge cutoff, open-weights badge, tags. Body: description; capability
chips tiered core/advanced/niche (modelwiki badges.go spec; Jan Capabilities
chips); per-provider offering table (Input/M, Output/M, Cache read/M, Context,
Availability, Lifecycle) = routing preview; params supported; lineage/family
cross-links ("Other models in claude-opus family"); "More from {author}".
CTA: Open in chat (seeds model), Add to comparison.

### D6. Traversal & search
- Models list: add author facet + tag facets, clickable rows -> detail;
  keep table; virtualize (@tanstack/react-virtual) + Fuse.js fuzzy (id, name,
  author) with debounce+useTransition (Jan pattern).
- CM13 command palette becomes the global traversal: models, providers,
  authors, pages, actions. (Sequence CM13 into this campaign.)
- Deep-linkable state: full routes for catalog entities; drawers w/ query
  params only for editable config (agentgateway useStickyQueryParam).

### D7. Composer retooling
- Presets live in the model picker ONLY (already listed there); remove the
  "+"-> Insert -> Presets popover.
- "+" becomes attachments menu per ChatGPT/Claude convention. VERIFIED: the
  gateway passes image content end-to-end (inference.ContentImage; both
  openai and openrouter codecs decode image_url / input_image parts). So
  phase 1 ships "+" = attach image (data URL), enabled only when the selected
  model has image input modality (console already derives this for capability
  badges). Web-search/deep-research chips deferred until the gateway has a
  real tools path (name the owner in the plan's out-of-scope).
- Model selector: model + presets + reasoning effort (model behavior).

### D8. Performance identity (gateway overhead)
- Measure per-request Starport overhead (total minus upstream time; for
  streams: pre-upstream + post-upstream processing) in internal/proxy.
- Response header `x-starport-overhead-ms` on every proxied response
  (LiteLLM's beloved trust feature; only vendor to copy it).
- Usage page: overhead column; Overview: "Starport overhead p50/p99" stat.
- Claim phrasing (README/marketing): "adds <X ms p99 gateway overhead
  (excludes provider inference time)" + published methodology; add a
  benchmark harness (mock upstream) to CI to keep the number honest.
- Chat MetadataLine: "ttft" -> "TTFT" (user directive).

### D9. Copy & brand
- Wordmark: STARPORT uppercase in sidebar (user directive; tracking-wide).
- Nav: "API Keys" (user's exact ask; note Stripe/Linear sentence-case
  convention "API keys" — follow the user: "API Keys").
- Provider/author display names from Starmap Name fields (fixes lowercase
  "ollama"/"mistral" cards).
- Metric vocabulary: TTFT, tok/s, p50/p99 (pick lowercase p, OpenRouter
  style, already used in console), "Uptime (3d)" style windows.

### D10. Seams & testing (architecture)
- New concept seam: catalog presentation projection. Today internal/proxy
  builds console DTOs inline (service.go). Extract a projection package
  (internal/catalog/view or internal/proxy/catalogview) owning
  ModelInfo/ProviderInfo/AuthorInfo assembly from snapshot — unit-testable
  against a fixture catalog, keeps proxy thin.
- Overhead measurement seam: a timing type in internal/proxy (or
  internal/execution) with contract tests (stream + non-stream).
- Console: components paired with contract-level tests where logic lives
  (facet filtering, fuse search config, logo fallback chain).
- Carry gateway follow-ups into plan: empty-completion caching bug;
  streaming 429 -> empty 200 stream normalization gap (internal/failure).

## Sequencing sketch (new durable plan, after CM12 merges)
CM13 (command palette, already spec'd) folds in as the traversal task.
Phase A (data): projection seam + widened DTOs + authors API + logos route;
  Starmap v0.7.0 (logo payload + lifecycle dates + coverage fill) in parallel.
Phase B (catalog UX): providers revamp + provider detail; model detail +
  models list facets; authors pages; command palette wiring.
Phase C (composer): presets->picker consolidation, "+" retool, TTFT label.
Phase D (performance): overhead measurement + header + console surfacing +
  benchmark harness + README claims.
Phase E: CM14 cutover + CM15 cleanup (existing campaign tail) + brand/copy
  sweep (STARPORT wordmark, API Keys, casing).

Open items to verify before plan freeze:
- Does proxy pass multimodal/image content today? (D7 scope)
- Starmap release process access (make release VERSION=0.7.0) — allowed per
  user ("feel free to release new versions of starmap as needed").
- lobe-icons licensing note for vendored set (MIT code; trademark nominative
  use) — document in NOTICE.
