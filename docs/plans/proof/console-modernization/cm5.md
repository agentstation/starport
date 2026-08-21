# CM5 proof: providers page

Date: 2026-08-21. Branch: `codex/cm-5-providers` on main @ d33adb1.

## What landed

- `console/src/routes/providers.tsx`: provider cards from the safe
  runtime snapshot (`/api/v1/admin/providers`) joined with catalog
  display names (`/api/v1/providers`). One status vocabulary per
  DESIGN.md: the dot is adapter liveness (`ready` green,
  `no_offerings` neutral, unsupported states red) and the pill is the
  operator credential lifecycle (ready/no credential/refreshing/
  denied/invalid/unavailable with semantic tints; updated-at in the
  tooltip). The legacy page's three mixed vocabularies and the
  40-dot offering matrix are gone.
- Credentialed providers sort first (usable, then configured but
  broken, then no credential; alphabetical within each rank).
- Offering counts are plain numbers; the offerings count links into
  the filtered models page (`/models?provider=<id>`), with a
  separate "N available" count. Credential and adapter reasons
  render as humanized text only when they explain a problem.
- Refresh (POST `/api/v1/admin/providers/refresh`) carried over with
  the inline-notice pattern: reports changed/unchanged and a real
  failure count.
- Non-admin fallback carried over: on 401/403 from the admin
  endpoint the page renders the catalog view (name, ID chip, model
  count linking into models) under a locked note.
- `console/src/lib/api.ts`: `ProviderStatus` widened to the full
  snapshot shape (revision, catalog generation, adapter/credential/
  offering projections); added `listProviderCatalog()` and
  `refreshProviders()`.
- `console/src/components/shell/Shell.tsx`: Providers nav entry
  flipped to implemented.

## Evidence

- `pnpm -C console check` → Vite build + `tsc --noEmit` clean.
- `bash scripts/verify-console-modernization.sh` → `Summary: 11
  passed, 10 failed`; CM-V09 newly passes. Fail-before recorded in
  cm0.md.
- Live render (`STARPORT_CONSOLE_NEXT=1`, embedded build, real
  gateway with a Groq inference credential in the environment):
  - Sort order: groq (ready/usable) and ollama (ready, credential
    not required) first, google-vertex (unavailable — its cloud
    credential source is genuinely unreachable here) next with the
    warning pill and reason "credential source unavailable", then
    twelve no-credential providers alphabetically.
  - Dot stayed orthogonal to the pill: azure-openai/mistral/ollama
    show the neutral "no offerings" dot regardless of credential
    state; adapter-ready providers show the green dot.
  - "17 offerings" on the Groq card navigated to
    `/models?provider=groq` with the filter select showing
    "groq (2)" and the count "2 of 422 models".
  - Refresh reported "Refresh finished with 1 failures" (the
    google-vertex source; pluralization fixed to "1 failure" after
    the capture) and the providers re-rendered from the invalidated
    query.
  - Catalog-only fallback verified with a real limited-scope key
    (403 on the admin endpoint, 200 on the public list): locked
    note plus catalog cards with model counts.
- Rendered proof: `cm5-providers-dark.jpg`, `cm5-providers-light.jpg`.
- Full gate suite on the branch: see the execution log row for CM5.
