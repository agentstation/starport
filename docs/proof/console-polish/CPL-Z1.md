# CPL-Z1 proof: close

Base: the CPL-G1 squash `3556f03`.

## What changed

| Path | Change |
| --- | --- |
| `.github/workflows/ci.yml` | Runs `verify-console-polish.sh` after the enterprise-readiness gate. |
| `AGENTS.md` | The required evidence list names `verify-console-polish.sh`. |
| `DESIGN.md` | The shell section describes the small-screen layout. The components section describes sheet sides. |
| `CHANGELOG.md` | New. The v1.2.0 summary for enterprise readiness, console polish, and fixes. |
| `console/README.md` | New. Stack, commands, test conventions, and the design evidence gate. |

## Walkthrough

The dev console at `127.0.0.1:5174` served the shipped build from `codex/cpl-g1`. A script visited every file route plus an author, a provider, and a model detail. It compared the document scroll width with the viewport width on each.

| Viewport | Routes | Wider than the viewport | Notes |
| --- | --- | --- | --- |
| 1440 px | 21 | 0 | `/auth` redirects to `/` while a session exists. |
| 375 px frame | 24 | 0 | The top bar and the navigation sheet render on every route. |

Open findings: none. Every route renders its page header, and no route scrolls sideways at either width.

## Counts

| Check | Before | After |
| --- | --- | --- |
| Verifier | 46 passed, 2 failed | 48 passed, 0 failed |
| Console tests | 405 | 405 |

## Fail-before

At the CPL-G1 squash the verifier prints `Summary: 46 passed, 2 failed`. V47 fails because CI does not run the gate. V48 fails because the evidence list does not name it.

## Commands

```
bash scripts/verify-console-polish.sh
pnpm typecheck
./node_modules/.bin/vitest run
pnpm build
```

Repository gates: 23 of 23 pass.
