# CPL-E2 proof: settings sections for the gateway as configured

Branch `codex/cpl-e2`. Base: the CPL-E1 squash `88d9be6`.

## What changed

| Owner | Change |
| --- | --- |
| `console/src/lib/api.ts` | `SystemInfo` types every CPL-E1 field: the build stamp, the start time, the platform, both store modes, telemetry, guardrails, and retention. `WebhookSummary` types the summary route, and `webhookSummary` reads it. |
| `console/src/lib/queries.ts` | `queries.webhooks()` owns the webhook summary query. |
| `console/src/lib/format.ts` | `formatRetention` states a window in the largest whole unit and names a zero window `no expiry`. |
| `console/src/components/settings/Section.tsx` | The flat settings section moved out of the route, so the route and the new panels share one shape. |
| `console/src/components/settings/Deployment.tsx` | Five read-only sections: System, Observability, Guardrails, Webhooks, and Retention. Each row states the value, an optional detail, and the environment variable that sets it. One gate turns the locked and failed states into one line per section. |
| `console/src/routes/settings.tsx` | The page mounts the five sections between Authentication and Appearance. The About section became Source and keeps the repository link alone. |
| `console/src/components/shell/Shell.tsx` | The sidebar names an unstamped build `dev build` instead of `vdev`. |

## Honesty rules

| Fact | Words on the page |
| --- | --- |
| Version or build time of `dev` | `unstamped build`, and no commit detail |
| Uptime of `unavailable` | `unavailable`, and no start detail |
| Traces `null` | `off` |
| Usage export `off` | `off`, and no drop count |
| Empty check list | `none` |
| Empty moderation model | `not set` |
| No receivers | `none configured`, and the signing secret reads `not set` |
| Retention of 0 seconds | `no expiry` |
| Admin plane refused | `Reading the <state> needs an admin-scoped key.` |

The page states only what the gateway answers. Every variable name on the page is the name the operator guide documents.

## Counts

| Measure | Before | After |
| --- | --- | --- |
| Console tests | 339 | 342 |
| Entry chunk | 118.67 kB gzip | 118.68 kB gzip |
| Verifier | 31 passed, 17 failed | 32 passed, 16 failed |

## Fail-before

At `5000da4` (origin/main) in a fresh worktree the verifier reported `FAIL CPL-V32` with 29 passed and 19 failed. The copied `settings.test.tsx` ran there and all three tests failed, because the page held none of the asserted text.

## Tests added

| File | Test |
| --- | --- |
| `console/src/routes/settings.test.tsx` | The first test renders both fixtures and asserts every value and every variable name. The second test renders an unstamped build with an empty webhook setup and asserts the words above. The third test answers 403 on both routes and asserts the locked line for each section. |

## Commands

| Command | Result |
| --- | --- |
| `pnpm typecheck` | Clean. |
| `pnpm exec vitest run` | 47 files, 342 tests passed. |
| `pnpm build` | Built. Entry chunk 118.68 kB gzip. |
| `bash scripts/verify-console-polish.sh` | 32 passed, 16 failed. V32 passes. |
| `bash scripts/verify-*.sh` (23 gates, release build gates excluded) | All 23 passed. |

## Visual check

The page rendered against a rebuilt local gateway on the vite dev server. Each section read its real value, the webhook section read `none configured`, and retention read `400 days`, `30 days`, and `1 day`.
