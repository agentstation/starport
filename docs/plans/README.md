# Durable plans

## Active

- [Starport catalog, performance, and brand campaign](catalog-performance-plan.html) —
  make the catalog a traversable provider/model/author graph with logos
  and detail pages, retool the composer, measure and publish the gateway
  overhead (`x-starport-overhead-ms`), sweep the brand (STARPORT, API
  Keys), fix the four gateway defects from Design Review No. 2, and cut
  the SPA over as the only console. Absorbs CM13–CM15 from the console
  modernization plan. Verifier: `scripts/verify-catalog-performance.sh`.
  Proof root: `proof/catalog-performance/`.
- [Starport console modernization](console-modernization-plan.html) —
  rebuild the embedded console as a React SPA (Vite 8, React 19,
  Tailwind 4, shadcn/ui, TanStack, Streamdown) driven by the `DESIGN.md`
  design system, move the chat model picker into the composer, and
  delete the legacy static console. CM0–CM12 delivered; becomes
  `superseded(catalog-performance-plan)` when CM12 merges (CM13→CP12,
  CM14→CP18, CM15→CP19). Verifier:
  `scripts/verify-console-modernization.sh`. Proof root:
  `proof/console-modernization/`.

## Order

The catalog, performance, and brand campaign is active. The CM12
closeout on the console modernization plan runs in parallel; when its
PR merges, that plan becomes `superseded(catalog-performance-plan)`.

## Proposed

No proposed durable plan exists.
