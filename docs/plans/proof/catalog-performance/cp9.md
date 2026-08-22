# CP9 — Model detail page

Date: 2026-08-22. Branch: `codex/cp9-model-detail`.

## What shipped

- `console/src/routes/models_.$modelId.tsx`: un-nested detail route at
  `/models/:modelId` (ids carry an encoded slash, e.g.
  `/models/openai%2Fgpt-oss-120b`). Header shows the author logo, model
  name, an `open weights` badge, a copyable canonical id, the
  description, context length, knowledge cutoff, and author links. A
  not-found state covers ids outside the current snapshot.
- `console/src/components/models/ModelDetail.tsx`: tiered capability
  chips (modalities / core capabilities / remaining parameters), the
  per-provider offering table (provider link, provider model id,
  context, max out, prompt / completion / cache-read prices per 1M,
  live circuit chip with catalog-availability fallback, lifecycle),
  lineage links, and the `Open in chat` / `Compare` CTAs.
- `console/src/routes/chat.tsx`: `?model=` seeds the composer model;
  `&compare=true` opens comparison mode with that model pre-selected
  (mount-once guard, in-memory compare state unchanged).
- `console/src/lib/modelFilter.ts`: extracted the models-list filter
  seam and fixed a defect the detail links depend on — the provider
  filter matched only the id prefix (the author), so
  `/models?provider=groq` missed most groq-served models. The filter
  now also matches serving providers from `offerings`, and the search
  haystack includes provider model ids.
- `internal/console/spa.go`: SPA fallback now covers `/models/*`.

## Contract tests

- `console/src/components/models/ModelDetail.test.tsx` — 8 tests:
  capability tiering (incl. `include_reasoning` → reasoning, empty
  model), CTA hrefs (`/chat?model=…`, `…&compare=true`), circuit join,
  offering-table rendering (provider link, per-1M price display,
  circuit chip vs availability fallback, empty state), lineage dedup.
- `console/src/lib/modelFilter.test.ts` — 3 tests: serving-provider
  match, provider-model-id search, `include_reasoning` capability.
- `internal/console/spa_test.go` — nested-path test covers
  `/models/meta%2Fllama-3.1-8b-instruct`.

## Verification

- `pnpm -C console test`: 5 files, 39 tests, 0 failures.
- `pnpm -C console typecheck`, `pnpm -C console build`: clean.
- `go test ./...` 0 failures; `go vet` clean; `make lint` 0 issues;
  `make build` ok; SDK smoke PASS.
- All 8 `scripts/verify-*.sh` gates PASS.
- `scripts/verify-catalog-performance.sh`: 12 passed. CPV08 (model
  detail route) now PASS. The 6 remaining failures are future tasks
  (CPV09–CPV14).

## Live acceptance

Dev gateway with `STARPORT_CONSOLE_NEXT=true`:

- `cp9-model-detail-dark.jpg` / `cp9-model-detail-light.jpg`:
  `/models/openai%2Fgpt-oss-120b` renders the full offering table —
  4 offerings (Cerebras, DeepInfra ×2, Fireworks AI) with distinct
  prices, cache-read price on Fireworks, live `healthy` circuit chips,
  the `open weights` badge, tiered capability chips, and the
  `family: gpt-oss` lineage link.
- `cp9-open-in-chat-dark.jpg`: clicking `Open in chat` lands in chat
  with `gpt-oss-120b` selected in the model picker.
