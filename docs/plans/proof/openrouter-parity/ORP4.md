# ORP4 — Console usage page

Status: done. PR: [#130](https://github.com/agentstation/starport/pull/130)
(`codex/orp-4-usage-page` → `codex/orp-3-activity`). Commit `4e5210e`.

## Fail before

`TestUsagePageIsRouted` was written first and run red against the
pre-change tree:

- `PagePaths` did not contain `/usage`
  (`[]string{"/", "/chat", "/models", "/providers", "/keys", "/settings"}`).
- The shell template had no `data-page="usage"` nav entry.
- `GET /static/js/pages/usage.js` returned 404 (module absent from the
  embedded assets).

A live check on the pre-change dev gateway confirmed `GET /usage`
returned 404.

## What landed

- `/usage` in `PagePaths`, a nav entry with a new `i-usage` icon, the
  route in `app.js`, and a new `pages/usage.js` module (~330 lines).
- Request-log table over the ORP3 activity API: time (relative, ISO on
  hover), model requested plus model used when they differ, key column
  under admin scope, provider, status badge with error class, total
  tokens, latency, cost, cache. Filters: model text, provider text,
  status select, time-range select (1h/24h/7d/30d/all, default 24h).
  Cursor paging: up to 5 pages eagerly, then a load-more control.
- Scope resolution: the page tries `/api/v1/admin/activity` first and
  falls back to `/api/v1/activity` on 401/403; the toolbar labels the
  scope "all keys" or "your key".
- Summary strip aggregated from loaded records: requests, errors,
  tokens, spend; a `+` suffix and "loaded so far" note when a cursor
  remains.
- Per-row detail drawer (shared `sidePanel` helper promoted from the
  models page into `ui.js`): request id, key, time, protocol,
  operation, models, provider, status with HTTP code, error class,
  attempts, routing and latency, cache, cost or its absence reason,
  and the token breakdown.
- Overview metrics card now renders real `/api/v1/admin/metrics` data:
  requests 24h with rate, errors with percentage, tokens, spend,
  latency p50/p95, and an "open usage" link.
- Spend honesty (invariant 3): when zero loaded records carry a cost
  object, the spend stat shows `—` with the "N without cost" count —
  never `$0`. Unpriced rows carry reason badges (`no pricing`,
  `no route`, `no usage`). The overview spend stat applies the same
  rule via `requests_without_cost`.

## Acceptance evidence

1. **Route test**: `TestUsagePageIsRouted` red before (3 assertion
   failures above), green after. `go test ./internal/console/ -count=1`
   ok.
2. **Real requests with costs surfaced honestly** (browser walkthrough,
   dev gateway, admin key): the table listed 13 real requests — 4 ok
   groq/compound-mini rows with token counts (551/557/477/553) and
   `no pricing` badges, 9 error rows with status badges
   (authentication, provider unavailable, quota) and `no route`
   badges. Summary: 13 requests, 9 errors, 2,138 tokens, spend `—`
   with "13 without cost".
3. **Filters refetch**: status=error narrowed to 9 rows and the summary
   refetched (9 requests, 9 errors, 0 tokens, "9 without cost");
   resetting to any status restored 13.
4. **Detail drawer**: clicking an ok row opened the drawer with request
   id, key, protocol `openrouter`, operation `chat`, model
   requested/used, provider groq, status ok (200), attempts 1, routing
   811ms, latency 881ms, cache MISS, cost "unavailable — no pricing",
   tokens "input 445 · output 106 · total 551".
5. **Overview no longer constant zeros**: the overview showed requests
   24h 13 (0/min now), errors 9 (69.2% of total), tokens 2,138, spend
   `—` ("13 without cost"), latency p50 65ms, p95 1.63s, and the
   "open usage" link.
6. **Empty and error states**: implemented in `usage.js` — no-key
   connect prompt, missing-scope hint on 401/403, the gateway's 503
   "not configured" message, filtered and unfiltered empty states. The
   unfiltered-empty and 503 branches were not exercised live on this
   gateway (it has traffic and a configured repository): UNVERIFIED
   beyond code review and the shared code paths.

Browser screenshots (session-local):
`claude-chrome-screenshots-6DpuD6/screenshot-1787242178726-0.jpg`
(usage table), `…-1.jpg` (status filter), `…-2.jpg` (detail drawer),
`…-3.jpg` (overview).

## Gates

All on `codex/orp-4-usage-page` at `4e5210e`: the seven
`scripts/verify-*.sh` gates exit 0 (including catalog-driven against
`../starmap`); `go test ./...` ok; `go vet` clean; `make lint` 0
issues; `make build` ok; `smoke-openrouter-sdks.sh` PASS for Python,
TypeScript, and Go. Autoreview (`--mode branch --base
origin/codex/orp-3-activity`, Sol high): clean, 0.98, no findings.

## Deviations and follow-ups

- **Spend-honesty fix mid-walkthrough**: the first build showed spend
  `$0` while all 13 records were unpriced. That violated invariant 3,
  so the summary now counts priced records and shows `—` when none
  carry a cost object. A true $0 cost (for example a fully cached
  request with a cost object) still renders `$0`.
- **Pre-existing chat.js defect observed** (not from this change):
  clicking the chat model/params popover buttons can throw
  `ReferenceError: Cannot access 'paramsPop'/'modelPop' before
  initialization` — the button handlers are wired near the top of
  `render()` but the `let modelPop`/`let paramsPop` declarations
  execute later, so a click before render reaches them hits the
  temporal dead zone. Fix when ORP12 reworks chat, or as a small fix
  on the console-revamp branch (#125).
- **model_used double-prefix** (`groq/groq/compound-mini`) still
  records upstream of the console; noted in ORP3.md, still open.
- The summary strip aggregates the loaded window (client-side over
  fetched pages), not a server aggregate; the overview card is the
  server-aggregate view. The plan's "real aggregates" requirement is
  satisfied by the overview card; the strip is labeled "loaded so far"
  whenever pagination truncates it.
