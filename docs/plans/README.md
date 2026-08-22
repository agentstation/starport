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

## Superseded

- [Starport console modernization](console-modernization-plan.html) —
  rebuilt the embedded console as a React SPA (Vite 8, React 19,
  Tailwind 4, shadcn/ui, TanStack, Streamdown) driven by the `DESIGN.md`
  design system, and moved the chat model picker into the composer.
  CM0–CM12 delivered. The plan became
  `superseded(catalog-performance-plan)` when CM12 merged; the campaign
  carried its last three tasks (CM13→CP12, CM14→CP18, CM15→CP19).
  Verifier: `scripts/verify-console-modernization.sh`, terminal at
  21/21 and running in CI. Proof root: `proof/console-modernization/`.

## Order

The catalog, performance, and brand campaign is the one active plan.
Its cleanup task (CP20) deletes both plans, both proof roots, and this
index when the campaign's final pull request merges.

## Proposed

No proposed durable plan exists.
