# CPL-B1 Query client defaults and query factories

The console reads every resource through one factory in
`console/src/lib/queries.ts`, and `main.tsx` sets the client defaults. The
verifier reports V01 and V02 green. The reading below dates from 2026-09-01
on branch `codex/cpl-b1` from `0d3ec47`.

## What changed

- `lib/queries.ts` holds one `queryOptions` factory per resource. Every key
  starts with a prefix no other factory uses. The usage activity key carries
  the window bound. Preset history has its own prefix.
- `main.tsx` sets the stale time, the cache time, the retry policy, and the
  window focus behavior once. The retry policy retries a network failure
  once and never retries a gateway answer.
- Every route and component spreads a factory into its query. No production
  file spells a key by hand or sets `retry: false`.
- The three credential save paths invalidate provider status with the list.
- The keys page reads every budgeted key detail through one `useQueries`
  call and passes each row its budgets.
- The settings page awaits its invalidation before it reports a valid key.
- The status hero, the model detail route, the provider detail route, and
  the BYOK panel narrow each read through `select`.
- The usage scope probe makes one request. A refused own listing surfaces
  as the activity query's error.
- A development build lazy-loads the query and router devtools from
  `devtools.tsx`. A production build emits no devtools chunk.

## Counts

| Item | Before | After |
| --- | --- | --- |
| Production `retry: false` sites | 71 | 0 |
| Hand-written `queryKey:` sites outside `lib/queries.ts` | 101 | 0 |
| Console tests | 213 in 33 files | 219 in 34 files |
| Entry chunk gzip | 91.71 kB | 91.69 kB |
| Verifier | 0 passed, 48 failed | 2 passed, 46 failed |

## Fail-before

V01 and V02 were red at `0d3ec47`, as `CPL-A0.md` records. The new
assertion in `SharedCredentialPanel.test.tsx` fails against the panel at
`0d3ec47` with `expected false to be true`, because the old save
invalidated the credential list alone.

## Tests added

- `lib/queries.test.ts` proves five facts. Every factory key starts with a
  unique prefix. The preset history key sits outside the preset list key.
  The activity key carries the window bound. The retry policy retries a
  network failure once. The scope probe makes one request and reads a 503
  as unconfigured.
- `SharedCredentialPanel.test.tsx`: a credential save invalidates provider
  status.

## Commands

```bash
pnpm -C console check
bash scripts/verify-console-polish.sh
grep -r "retry: false" console/src | grep -v "\.test\." | wc -l
```
