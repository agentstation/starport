# CM3 proof: overview page

Date: 2026-08-21. Branch: `codex/cm-3-overview` on main @ d68af6f.

## What landed

- `console/src/lib/api.ts`: typed gateway client. Key storage under
  `starport.apiKey` with change listeners, `ApiError` carrying status
  and parsed body (`needsKey` distinguishes 401/403 from failures),
  and endpoints for health, models, system info/metrics, provider
  status, catalog metadata, and admin activity.
- `console/src/lib/format.ts`: `formatCount`, `formatMs`,
  `formatNanoUSD`, `formatRelativeTime`, `shortGenerationID` —
  behavior matches the legacy console formatters exactly.
- `console/src/lib/useApiKey.ts`: `useHasApiKey()` via
  `useSyncExternalStore`, so locked cards unlock the moment a key is
  stored.
- `console/src/components/ui/Card.tsx` + `CopyButton.tsx`: the first
  shared primitives (panel card with title/aside, clipboard button
  with a 1.5s confirmation state).
- Overview composition (`console/src/routes/index.tsx` +
  `components/overview/`): StatusHero (readiness dot, origin,
  version, storage, uptime, model count), EndpointsCard (OpenAI `/v1`,
  OpenRouter `/api/v1`, health URL, copy buttons), QuickstartCard
  (curl/python/javascript tabs), StatsRow (six 24h stats from
  `/admin/metrics` with hourly sparklines bucketed from
  `/admin/activity`; unpriced spend shows "without cost", never $0),
  ProvidersCard (known/credentialed/usable), CatalogCard (generation,
  freshness, sequence, availability revision), ConnectCard (key entry
  when no key is stored). Admin-only cards render a locked message on
  401/403 instead of an error.
- Sparklines are neutral Text 4 polylines (72×20) — trend shape only,
  no axes, hidden when flat or empty, per DESIGN.md's restraint rule.

## Evidence

- `pnpm -C console check` → Vite build + `tsc --noEmit` clean.
- `bash scripts/verify-console-modernization.sh` → `Summary: 9
  passed, 12 failed`; CM-V07 (overview components imported by the
  index route) newly passes. Fail-before recorded in cm0.md.
- Live render (`STARPORT_CONSOLE_NEXT=1`, embedded build, real
  gateway state): stats row showed 43 requests / 29 errors / 1,858
  tokens / p50 66ms / p95 1.70s with three sparklines; providers
  15 known / 2 credentialed / 1 usable; catalog generation
  `catalog-20260819T233…` with relative freshness; hero read
  "Gateway ready · localhost:8080 · v1.0.0 · badger storage ·
  422 models". Stat values rendered in Geist Mono.
- Rendered proof: `cm3-overview-dark.jpg`, `cm3-overview-light.jpg`
  (light also verifies the expanded sidebar after a collapse
  round-trip).
- Full gate suite on the branch: see the execution log row for CM3
  (all repo verify scripts, go test/vet, lint, build, SDK smoke).
