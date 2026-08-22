# CP5 — Logo route and fallback chain

Date: 2026-08-22. Branch: `codex/cp-5-logos`.

## Scope

Render catalog identity offline through a fallback chain.

- `internal/catalog/logos`: embedded interim SVG set — 15 provider and
  41 author marks keyed by catalog ID — vendored from lobe-icons 1.94.0
  (MIT) and simple-icons 16.28.0 (CC0-1.0) with `NOTICE.md` entries.
  `Bytes(kind, id)` validates the ID shape, returns the SVG and a
  content hash, and reports unknown marks.
- `GET /api/v1/logos/{kind}/{id}.svg`: public route (registered in its
  own group so plain `<img>` tags work; every other `/api/v1` route
  keeps the API-key middleware), `Cache-Control: public, max-age=86400`,
  `ETag` with `If-None-Match` → 304, unknown IDs → 404.
- `console/src/components/catalog/EntityLogo.tsx`: bundled gateway SVG →
  tinted monochrome (inlined `currentColor` marks inherit the theme text
  color) → two-letter initials; one fetch per mark per session; wired
  into both provider card variants. No external URL is ever fetched.
- First console test runner: vitest + jsdom + testing-library,
  `pnpm -C console test`.

## Fail-before evidence

- `curl /api/v1/logos/providers/openai.svg` against the pre-CP5 gateway
  returned 404.
- `CPV05` and `CPV06` were red.

## Gates

- `go test ./...` — ok, zero failures (logos seam: 4 new tests;
  controller seam: 3 new tests)
- `pnpm -C console test` — 6 passed (every chain step, fetch-failure
  fallback, per-mark fetch dedup, initials derivation)
- `pnpm -C console typecheck` and `pnpm -C console build` — clean
- `go vet ./...` — clean; `make lint` — 0 issues; `make build` — ok
- `verify-starmap-ownership.sh`, `verify-v1-architecture.sh`,
  `verify-dependency-direction.sh` (+ self-test),
  `verify-package-layout.sh`, `verify-openrouter-parity.sh`,
  `verify-catalog-driven-providers.sh`, `verify-readme-quickstart.sh` —
  all passed
- `smoke-openrouter-sdks.sh` — TypeScript and Go SDK PASS
- `verify-catalog-performance.sh` — 8 passed (CPV05 and CPV06 newly
  green), 10 failed as scoped to later tasks

## Live acceptance evidence

Ephemeral dev gateway built from this change (loopback, in-memory,
since shut down):

- `GET /api/v1/logos/providers/openai.svg` → 200, `image/svg+xml`,
  `Cache-Control: public, max-age=86400`, without credentials.
- `GET /api/v1/logos/authors/qwen.svg` with the returned `ETag` in
  `If-None-Match` → 304.
- `GET /api/v1/logos/providers/no-such.svg` → 404 "Logo not found".
- `GET /api/v1/models` without a key still → 401, so the public logo
  group did not loosen the authenticated surface.
