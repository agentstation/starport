# CP7 — Providers page revamp

Date: 2026-08-22. Branch: `codex/cp7-providers-revamp`.

## Scope

Compact identity-first provider cards, one status treatment, page
search and sort, and whole-card links into a provider detail route.

- `console/src/components/providers/ProviderCard.tsx`: card seam
  extracted from the route file. Row one holds the logo, the display
  name, and the inline muted mono slug; the description clamps to two
  lines; the credential pill is the single always-present status and
  the adapter dot appears only on a fault (`state !== "ready"`); the
  footer copy reads "N models · M available"; the whole card is a link
  to `/providers/$providerId`. `CatalogProviderCard` gives the
  no-admin-key view the same treatment. `credentialRank` moved here.
- `console/src/routes/providers.tsx`: renders through the extracted
  components and adds a search field (matches id, name, and
  description) plus a sort control (status, name, models). The
  no-results state names the query.
- `console/src/routes/providers_.$providerId.tsx`: CP7 detail stub —
  identity header, description, credential pill, counts, Browse models
  and Documentation links. The `providers_.` un-nested spelling is
  required: the list route has no `Outlet`, so a nested child would
  render nothing. CP8 completes this page.
- `internal/console/spa.go`: `spaPagePaths` extends the shared
  `PagePaths` with `/providers/*` so the gateway serves the SPA shell
  for nested detail paths. Fail-before: `GET /providers/groq` on the
  running gateway returned the JSON `not_found_error` body.
- `console/src/lib/api.ts`: `ProviderCatalogEntry.docs_url`.
- `availableOfferings`: the available count now reads the circuit
  states the gateway actually reports (`healthy` and `half_open` admit
  attempts; `open` and `unavailable` reject). The page previously
  compared against a nonexistent `"available"` state, so every card
  showed "0 available" against 613 live healthy offerings.
- `scripts/verify-catalog-performance.sh`: `either_file` accepts any
  number of candidates; CPV07 accepts the un-nested route spelling.

## Fail-before evidence

- Pre-CP7 DOM rendered the slug as a chip on its own row, footer copy
  read "N offerings", and cards did not link anywhere.
- `CPV07` was red; `GET /providers/groq` returned the API 404 JSON.

## Gates

- `pnpm -C console test` — 16 passed (10 new ProviderCard tests:
  inline slug placement with chip-style regression guard, "N models ·
  M available" copy, whole-card href, single-status treatment,
  no-credential and invalid-credential states, fault-only adapter dot,
  catalog card, credentialRank ordering, circuit-state availability
  counting)
- `pnpm -C console typecheck` and `pnpm -C console build` — clean
- `go test ./...` — ok (new `TestSPAHandlerServesIndexForNestedPagePaths`)
- `go vet ./...` — clean; `make lint` — 0 issues; `make build` — ok
- All eight `scripts/verify-*.sh` gates — PASS
- `smoke-openrouter-sdks.sh` — Python, TypeScript, and Go SDK PASS
- `verify-catalog-performance.sh` — 11 passed (CPV07 newly green),
  7 failed as scoped to later tasks

## Live acceptance evidence

Dev gateway built from this change with `STARPORT_CONSOLE_NEXT=true`
on `127.0.0.1:8080`, driven headless (Chrome via playwright-core):

- `cp7-providers-dark.jpg` / `cp7-providers-light.jpg` — both themes:
  compact cards with logo, name, inline slug, two-line description,
  one credential pill, "N models · N available" footer; search and
  sort controls above the grid.
- `cp7-providers-search-dark.jpg` — query "open" narrows the grid
  live across id, name, and description.
- `cp7-provider-detail-dark.jpg` — `/providers/groq` serves the SPA
  shell and renders the detail stub: back link, identity header,
  description, credential pill, "17 models · 0 available", Browse
  models and Documentation links.
