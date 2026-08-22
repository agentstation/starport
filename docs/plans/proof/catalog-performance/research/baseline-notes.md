# Design review v2 — current-state baseline notes (2026-08-21)

Screenshots: review-{overview,providers,models,keys,presets,usage,settings}.jpg
(dark theme, 1471x1150, SPA @ STARPORT_CONSOLE_NEXT=1, main @ 7e71726 + CM12 branch build).

## Global / shell
- Wordmark: "Starport" title-case + amber spark icon. USER WANTS: uppercase
  STARPORT wordmark (like previous version). High-trust enterprise tone.
- Nav: Overview, Chat, Models, Providers, Keys, Usage, Presets, Settings.
  USER WANTS: "Keys" -> "API Keys". Nav order mixes catalog (Models/Providers)
  with ops (Usage) and config (Keys/Presets/Settings) without grouping.
- No global search / command palette yet (CM13 pending) — traversal between
  providers <-> models <-> authors is impossible today: nothing is clickable
  across entities.
- Footer: "Gateway healthy · v1.0.0", Light theme toggle, GitHub, Collapse. Fine.

## Overview
- Good: status hero, endpoints + quickstart snippets, KPI row (Requests 24h,
  Errors, Tokens, Spend, Latency p50 148ms / p95 5.45s), providers counts,
  Starmap catalog card.
- Missing: NO gateway-overhead metric (user directive: always show the latency
  Starport itself adds). Latency shown is end-to-end (dominated by provider).
  Need "Starport overhead p50/p95" (router+proxy time minus upstream time).
- Errors "41 · 31.8% of total" is alarming without cause breakdown; no link to
  filtered usage view.
- "Spend —  100 without cost" cryptic. Providers card "Known 15 / Credentialed
  4 / Usable 3" — good numbers, not clickable.

## Providers (user's focus)
- 2-col cards: name (catalog-cased, e.g. "ollama", "mistral"), slug chip on its
  OWN ROW under the name (user complaint: wastes space), corner status badge
  (ready / no credential / unavailable) AND inline status line "ready · N
  offerings · 0 available" (status duplicated; "0 available" confusing).
- No logos. No click-through to a provider detail page. No search/sort/filter.
- Needed: compact card w/ logo + name + slug inline; provider detail page
  (offerings list, credential state, endpoints, latency/error stats, docs link).

## Models
- Generation banner (catalog id, "generated 2d ago", no manifest, sequence,
  revision, what-changed, refresh) — good ops surface, visually heavy at top.
- Search + All providers / modalities / capabilities dropdowns; "422 of 422".
- Table rows: mono id chip + display name (often duplicates id), capability
  chips (image/tools/reasoning), context, "price / 1M" ("$0.8 in · $4 out").
- Rows NOT clickable — no model card/detail view (user wants one: pricing per
  provider, context, capabilities, author, offerings comparison).
- No author dimension anywhere (user: authors exist in catalog, surface them).

## Keys
- Title/nav "Keys" -> "API Keys". Table fine: name, masked key + copy, scope
  chips, limits (budget/day + tok/day with progress bars + "left"), status,
  created, byok/edit/disable/delete.
- Limits cell cramped (wrapping "1,000 tok left"). No per-key usage link.

## Presets
- Single table (@preset/fast-groq): mono chip + description, model, routing
  chips (sort price, order groq), overrides, updated, edit/delete.
- USER QUESTION: presets shouldn't be the composer "+" — they're already in
  the ModelPicker. "+" should be tools/web search/deep research attachments.

## Usage
- Filter bar (model, provider, key ID, status, time range), 4 KPI sparkline
  cards (Requests w/ error overlay, Tokens, Spend, Latency 1.03s avg), request
  table: time, model (+resolved route subtext), key, provider, status pill
  (ok / rate limited / provider unavailab[le] — truncated!), tokens, latency,
  cost ("no pricing" / "no route" amber pills — noisy; cache hit/miss).
- Latency column is end-to-end only; no gateway-overhead column, no TTFT.
- "no route" pill in COST column is a category error (it's a routing outcome).
- No row expansion/detail (request inspector), no export.

## Settings
- Connection (replace key, save & test, current key chip), Appearance
  (Dark/Light/System), Chat data (export/delete), About (gateway/version/
  storage + repo link). Reasonable; low priority.

## Chat composer (from code, Composer.tsx)
- "+" = "Insert" button -> popover listing Presets only. Move presets fully
  into ModelPicker (already listed there); repurpose "+" per user (attachments,
  web search, tools) or remove until tools exist.
- MetadataLine: "ttft" lowercase -> "TTFT" (user directive), tok/s, cache miss.

## Cross-cutting user directives (v2 campaign)
- Provider logos (air-gapped: must be bundled or gateway-served, no CDN).
- Entity graph traversal: provider card <-> models <-> author pages + search.
- Author/lab pages (data exists in Starmap catalog; may need projection work
  + possibly Starmap release).
- Performance identity: measure + always show Starport-added overhead;
  TTFT capitalization; publishable performance claims.
- Enterprise-trust polish: uppercase STARPORT wordmark, "API Keys", consistent
  casing of provider names (Starmap display names), quality seams + tests.

## Gateway findings from live investigation (2026-08-21 evening)
1. google-ai-studio never configures: ambient GEMINI_API_KEY / GOOGLE_API_KEY /
   GOOGLE_GENERATIVE_AI_API_KEY are ignored, AND explicit
   STARPORT_GOOGLE_AI_STUDIO_API_KEY_REFERENCE="env:GEMINI_API_KEY" is ignored.
   State stays not_configured/credential_not_configured. No server log line, no
   failure entry in the refresh report. v0.6.0 embedded catalog DOES list the
   env aliases; suspicion: the generation-store payload drops credential
   environment lists / profiles, or resolveProviderRuntime skips silently.
   Two defects: (a) resolution gap, (b) zero operator-visible evidence for
   "why is my provider not configured" — enterprise-trust killer.
2. Response cache stores empty upstream completions (up-arrow 0) for full TTL.
3. groq daily-token 429 on the STREAMING path surfaces as an empty 200 stream
   (internal/failure normalization gap); non-streaming path returns proper 429.
4. Refresh report shows failure_count but /admin/providers/refresh response
   carries no failure detail (which provider, why) — the UI can't show it.
