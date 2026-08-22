# CP1 proof — brand sweep

Date: 2026-08-21 (CDT). Branch: `codex/cp-1-brand-sweep`, rebased on
main @ 1f596de (CP0 merge, PR #157).

## Change

- Sidebar wordmark renders `STARPORT` (uppercase, 600 weight, 0.08em
  tracking). `DESIGN.md` records it as the one uppercase display
  treatment.
- Navigation item and keys page heading read `API Keys`. The chat budget
  error message points to the API Keys page.
- Chat metadata badge capitalizes `TTFT`.
- `providerLabel(id, name)` in `console/src/lib/format.ts` is the one
  place provider display names resolve: catalog `name` wins, raw id is
  the fallback, no case transforms. Used by the providers cards, models
  provider dropdown, usage request detail, and the chat model picker.
- Credential pills get `shrink-0 whitespace-nowrap`, so long provider
  names no longer squeeze them.

## Evidence

- `bash scripts/verify-catalog-performance.sh` → `Summary: 3 passed, 15
  failed`. Green: CPV15 (TTFT), CPV16 (STARPORT), CPV17 (API Keys). The
  15 failures are later-task conditions, unchanged from the CP0 red
  baseline.
- `pnpm -C console build` ✓ and `npx tsc --noEmit` ✓. The console has no
  unit-test script; build + typecheck are the repository-owned checks
  for console-only changes.
- Screenshots (this directory), captured against `starport dev`
  (in-memory, `:8081`, console-next) with Playwright headless Chrome at
  1471×1150:
  - `cp1-providers-dark.jpg` — wordmark, API Keys nav, catalog display
    names (Alibaba Cloud Model Studio, Google Vertex AI, Groq, Moonshot
    AI), non-truncated credential pills, dark theme.
  - `cp1-providers-light.jpg` — same page, light theme.
  - `cp1-chat-ttft.jpg` — seeded thread metadata line: `TTFT 312ms ·
    1.84s total · 42.3 tok/s · ↓18 ↑64`.

## Observation for CP3/CP6

The catalog has no display `name` for `ollama`, `azure-openai`, and
`mistral`; those cards render the slug fallback. That is a Starmap
coverage fill for the v0.7.0 release task (CP6), not a console defect.
