# CPL-F5 proof: docs page re-sync with a navigation coverage test

Base: the CPL-F4 squash `8897619`.

## What changed

| Path | Change |
| --- | --- |
| `console/src/routes/docs.tsx` | Sections render flat with a rule between them instead of as cards. |
| `console/src/routes/docs.tsx` | The build persona gains a JavaScript snippet, a batch snippet, and a "Use it from a coding agent" section. |
| `console/src/routes/docs.tsx` | The Python and JavaScript snippets read `STARPORT_API_KEY` from the environment. |
| `console/src/routes/docs.tsx` | The account persona gains a scope table that names `moderations:write` and `batches:write`. |
| `console/src/routes/docs.tsx` | The operate persona gains Policy, Teams and budgets, Audit log, and Observability sections with origin snippets. |
| `console/src/routes/docs.tsx` | The health section cites `/health/live` and `/health/ready` and links the Overview page. |
| `console/src/routes/docs.tsx` | Every sidebar destination has a link: Chat, Authors, Members, Teams, Audit log, and Overview were missing. |
| `console/src/routes/docs.test.tsx` | New. Three tests: sidebar coverage, health paths and scopes, and the key variable in every snippet. |
| `console/src/components/shell/Shell.tsx` | `NAV_SECTIONS` is exported so the coverage test reads the sidebar's own list. |
| `console/src/routes/usage.tsx` | The empty-state snippet names `STARPORT_API_KEY` instead of `STARPORT_KEY`. |

## Design notes

The coverage test imports the sidebar list from `Shell.tsx` rather than a copy. A page added to the shell without a docs sentence fails the test. The test scopes its link search to the open tab panel, because the sidebar itself links every destination.

The Base UI tab panel unmounts inactive personas. The test clicks each tab and waits for `aria-selected` before it reads the panel.

The team budget snippet uses a PUT with a `month` interval and a nano-USD limit. Those are the values `TeamDetailPanel.test.tsx` and the `Team` type in `lib/api.ts` accept.

The keys page still names `STARPORT_ADMIN_KEY` in its admin snippet. That snippet documents an admin key, a different credential, so it stays.

## Counts

| Check | Before | After |
| --- | --- | --- |
| Console tests | 399 | 402 |
| Console test files | 63 | 64 |
| Main chunk gzip | 119.17 kB | 119.17 kB |
| Verifier | 43 passed, 5 failed | 45 passed, 3 failed |
| Bare `/health` citations in `docs.tsx` | 1 | 0 |

## Fail-before

At `8897619` with `docs.test.tsx` and the `Shell.tsx` export copied in, the suite reports 2 failed and 1 passed. The coverage test fails because six destinations have no link: Overview, Chat, Authors, Members, Teams, and Audit log. The health test fails because the page cites a bare `/health` path and no scope table exists. The snippet test passes at baseline, because the two baseline snippets already name `STARPORT_API_KEY`.

## Commands

```
pnpm typecheck
./node_modules/.bin/vitest run
pnpm build
bash scripts/verify-console-polish.sh
grep -E "/health([^/]|$)" console/src/routes/docs.tsx | wc -l
```

The verifier prints `Summary: 45 passed, 3 failed`. The remaining red checks are V46, V47, and V48, which CPL-G1 and CPL-Z1 own. The health grep prints 0.

Repository gates: 23 of 23 pass.

## Visual check

The dev console at `127.0.0.1:5174/docs` renders the three personas flat with a rule between sections. The operate persona shows Policy, Teams and budgets, Audit log, Observability, and the two health paths. The snippets carry the console origin and `STARPORT_API_KEY`. The account persona shows the scope table.
