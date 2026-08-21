# ORP8 — Provider preference completion and variants

Branch `codex/orp-8-provider-prefs` (from `codex/orp-7-presets`), PR #134.

## Fail-before evidence

Captured with temporary probe tests (created, run, deleted) before any fix:

1. Planner probe with model `author/primary:floor`:
   `fail-before: err=no route candidate satisfies the request: 0 route(s) rejected rejections=0`
   — a variant-suffixed model produced a zero-rejection `ErrNoCandidate` with
   no recorded reason.
2. Decode probe with `provider.quantizations`:
   `fail-before: err=json: unknown field "quantizations"`
   — a documented OpenRouter field was a hard 400 through the strict decoder.

## What landed

- `internal/routing`: `ProviderPolicy` gains per-token price caps;
  `Request.ZeroPriceModels` carries `:free` constraints; new rejection codes
  `price_exceeded` and `unknown_model`. The planner records which requested
  models matched a candidate and emits one `unknown_model` rejection per
  requested model that matched nothing, so a failed plan always reports
  reasons. `rejectPrice` enforces caps and the zero-price constraint; both
  reject unknown-price routes ("a cap is a promise the planner can only keep
  with known prices").
- `internal/protocol/openrouter`: wire `ProviderPreferences` gains
  `quantizations` and `experimental`; `max_price` became a typed `MaxPrice`
  object (JSON number or numeric string, strict keys); `sort` is validated
  against `price|latency|throughput`; `DecodedChat.UnenforcedProviderFields`
  lists the D3 fields the request used, sorted.
- `internal/router`: `ProviderPreferences` gains `Sort` and per-1M price
  caps; `parseModelVariants` strips `:floor`/`:nitro`/`:free` (unknown
  suffixes stay opaque); `plannerOptimization` resolves sort precedence
  (explicit sort > variant > server default); the registry fallback rejects
  `:free` loudly (no price facts) and `filterByProviderPreferences` was
  rewritten with catalog-planner semantics — `only`∧`ignore` compose (legacy
  quirk fixed), `order` without fallbacks keeps only ordered providers,
  case-insensitive names.
- `internal/proxy` + `internal/response/cache`: sort and price caps flow
  through `transformProviderPreferences` and join the cache identity
  (`Policy.Provider`), so differently-routed requests never share an entry.
- `internal/server/controllers`: OpenRouter decode maps sort/max_price into
  proxy preferences and sets `X-Starport-Unenforced-Provider-Fields` on the
  response (stream and non-stream).

## Acceptance evidence

All named acceptance tests red-first then green:
`TestProviderSortPrice`, `TestProviderSortLatency`,
`TestMaxPriceRejectsExpensiveRoutes`, `TestQuantizationsAccepted`,
`TestVariantFloorSortsByPrice`, `TestVariantFreeFiltersZeroPrice`,
`TestUnmatchedModelReportsRejection`, `TestOnlyAndIgnoreCompose`, plus
`TestUnenforcedProviderFieldsHeader` at the server seam.

## Gates

- 7/7 `scripts/verify-*.sh` exit 0 (catalog-driven 19/19).
- `go test ./...` exit 0; `go vet` exit 0; `make lint` 0 issues; `make build`
  exit 0.
- `scripts/smoke-openrouter-sdks.sh`: PASS Python, TypeScript, Go.
- Autoreview (Sol, high, branch vs `origin/codex/orp-7-presets`): clean,
  0 findings, overall 0.97.

## Deviations and deferrals

- `throughput` sorts by measured latency: Starport measures latency, not
  throughput (D4 deviation, documented in code and PR).
- Preset-stored `sort`/`max_price` (extending `presets.ProviderPreferences`)
  deferred to ORP9 with the console preset controls.
- `:floor`/`:nitro` on the registry fallback strip but have no ordering
  effect — the fallback has no price or latency facts; only `:free` fails
  loudly because it is a billing promise.
