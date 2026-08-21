# CM7 proof: usage page

Date: 2026-08-21. Branch: `codex/cm-7-usage` on main @ 400cc84.

## What landed

- `console/src/routes/usage.tsx`: the usage page from the activity
  API. Scope resolution tries admin activity first, then falls back
  to own-key activity, and renders dedicated states for a locked
  scope and an unconfigured usage repository. Filters: model text
  ("/" focuses it), provider text, key ID text (admin scope only),
  status select (ok / error / cancelled), and a range select (1h /
  24h / 7d / 30d / all time). Every filter lives in the URL through
  `validateSearch`, so filtered views are shareable links. Text
  filters debounce 300ms.
- Four chart cards over 32 fixed-width buckets of the loaded
  window: Requests (stacked bars, ok and cancelled on the neutral
  scale, errors on the error token), Tokens (neutral area), Spend
  (neutral area, dollar axis, "N w/o cost" count beside the total),
  and Latency (neutral average line, sparse buckets connected with
  small dots). Charts follow the DESIGN.md chart contract: neutral
  series from the text scale, semantic color only for the error
  series, faint gridlines, mono tabular axis labels, popover-ground
  tooltips. Headline values show a "+" when older pages remain.
- Dense virtualized table (40px rows): absolute times while a range
  is active (relative for all time), requested model with the used
  model under it, key chip (admin scope), provider, status pill
  (402 renders "budget exhausted", 429 renders "rate limited",
  other errors humanize `error_class`), tokens, latency, cost or a
  warning-tint reason chip (`no pricing` / `no route` / `no
  usage`), and cache hit/miss. Rows open a request detail side
  panel with the full record: IDs, timing, protocol, operation,
  models, provider, status, attempts, routing/latency split, cache,
  cost (or the unavailable reason), and the token breakdown with an
  estimated marker.
- Pagination: 200-record pages, auto-fetch to 5 pages, then a
  "Load older requests" button. Summary line reports the loaded
  count with a partial "+" suffix plus the scope (all keys / one
  key / your key).
- `console/src/components/ui/Chart.tsx`: chart tokens (`CHART`,
  `AXIS_TICK`) mapping series colors to role tokens, `ChartCard`,
  and the `ChartTip` tooltip on the popover contract.
- `console/src/lib/api.ts`: `ActivityRecord` widened to the full
  usage record shape, `ActivityFilters`, `listActivity` (own-key),
  and filter support in `listAdminActivity`.
- `recharts` 3.10.1 added; the chart bundle stays in the lazy-loaded
  usage chunk (406.92 kB, 115.51 kB gzip), so other routes pay
  nothing for it.
- `console/src/components/shell/Shell.tsx`: Usage nav entry flipped
  to implemented.

## Evidence

- `pnpm -C console check` → Vite build + `tsc --noEmit` clean.
- `bash scripts/verify-console-modernization.sh` → `Summary: 13
  passed, 8 failed`; CM-V11 newly passes. Fail-before recorded in
  cm0.md.
- Live verification (`STARPORT_CONSOLE_NEXT=1`, embedded build, real
  gateway, admin key, real groq credential):
  - Seeded traffic through the gateway: successful chats against
    `groq/compound-mini` and `groq/compound` (200s with token
    counts), plus real 429 rate-limit, 401 authentication, and 503
    provider-unavailable records. Canonical model IDs are
    author-prefixed, so `groq/llama-3.3-70b-versatile` correctly
    records `no models available for routing` — environmental, not
    a defect.
  - Page rendered 35 requests: charts showed 35 requests / 24 err,
    2,067 tokens, $0 with 32 w/o cost, 359ms avg latency. Status
    pills rendered ok (success tint), rate limited, authentication,
    and provider unavailable; cost reason chips rendered `no
    pricing` on priced-model gaps and `no route` on failures.
  - Row click opened the detail panel with the full record,
    including the routing/latency split (810ms routing of 864ms
    total) and the token breakdown (input 453 · output 88 · total
    541).
  - Status filter set to error → URL became `/usage?status=error`,
    table dropped to the 24 error rows, and all four charts
    recomputed from the filtered set.
  - Light theme verified: neutral chart series, faint grid, and
    pill tints hold on the light ground.
- Rendered proof: `cm7-usage-dark.jpg`, `cm7-usage-light.jpg`.
- Full gate suite on the branch: see the execution log row for CM7.
