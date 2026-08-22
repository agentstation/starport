# CP8 — Provider detail page

Date: 2026-08-22. Branch: `codex/cp8-provider-detail`.

## Scope

The CP7 detail stub becomes a full provider page: identity with policy
facts, a credential panel with a fix-it path, a live health panel, and
a served-models table.

- `console/src/components/providers/ProviderDetail.tsx`: new detail
  seam. `PolicyChips` renders only catalog-declared data-handling
  facts (retention, training, moderation, headquarters); an absent
  field stays silent. `CredentialPanel` states the operator credential
  lifecycle and, when the credential is missing or broken, names the
  exact environment variables (`<PROVIDER>_API_KEY`, then
  `STARPORT_<PROVIDER>_API_KEY`, the README order) and links to the
  API Keys page for BYOK. `HealthPanel` shows the circuit-state
  breakdown over the provider's offerings plus a rolling one-hour
  window from the activity log: request count, error rate, p50 and
  p95 latency. `OfferingsTable` lists every offering with its circuit
  chip and reason, each row linking into the models list filtered to
  this provider (`/models?provider=…&q=…`) until CP9 builds the model
  detail page.
- `console/src/routes/providers_.$providerId.tsx`: wires the seams.
  The activity query mirrors the usage page's scope rule: admin keys
  read the cross-key log, other keys fall back to their own activity,
  and a locked log leaves the panel on circuit state alone. The
  header adds Website (`entry.url`) beside Documentation.

## Fail-before evidence

- Before CP8 the route was the CP7 stub: no policy facts, no
  credential fix-it path, no health data, no served-models table.

## Gates

- `pnpm -C console test` — 28 passed (12 new ProviderDetail contract
  tests covering both acceptance states: policy chip mapping and
  silence, operator env-name derivation, usable credential without a
  CTA, unconfigured credential with env names and the `/keys` link,
  broken credential with its reason, rolling stats math, empty-window
  stats without fake latencies, circuit breakdown ordering, health
  panel with and without traffic, offering row links)
- `pnpm -C console typecheck` and `pnpm -C console build` — clean
- `go test ./...` — ok; `go vet ./...` — clean; `make lint` — 0
  issues; `make build` — ok
- All eight `scripts/verify-*.sh` gates — PASS
- `smoke-openrouter-sdks.sh` — PASS
- `verify-catalog-performance.sh` — 11 passed, 7 failed as scoped to
  later tasks

## Live acceptance evidence

Dev gateway built from this change with `STARPORT_CONSOLE_NEXT=true`
on `127.0.0.1:8080`; four completions routed through Groq seeded the
health window before capture:

- `cp8-detail-configured-dark.jpg` / `cp8-detail-configured-light.jpg`
  — `/providers/groq` configured: ready credential, policy chips,
  health panel with 4 requests, 0.0% error rate, p50 452ms, p95
  880ms, 17 healthy served models.
- `cp8-detail-unconfigured-dark.jpg` — `/providers/deepinfra`
  unconfigured: "no credential" pill, CTA naming
  `DEEPINFRA_API_KEY` / `STARPORT_DEEPINFRA_API_KEY` with the Manage
  API Keys link, health panel reporting no requests in the window.
