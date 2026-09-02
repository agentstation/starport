# CPL-B2 Router context, loaders, preload, and route states

Every route warms its reads through a loader, and the router owns the
pending, error, and not-found states. The verifier reports V03, V04, and
V05 green. The reading below dates from 2026-09-01 on branch `codex/cpl-b2`
from `401be4e`.

## What changed

- `lib/api.ts` exports `ReadOptions`. Every read function accepts the abort
  signal and passes it to the request. A route change ends the requests the
  new route does not need.
- Every `queryFn` in `lib/queries.ts` passes the query signal to its read.
  The `settle` helper warms several reads for one loader and swallows a
  failed read. A page then keeps its own lock state after a refused read.
- `router.tsx` builds the router once with the query client in context.
  It sets intent preload, scroll restoration, and the default pending,
  error, and not-found components. The root route declares the context type.
- Every list route declares a loader that warms its reads. The model,
  provider, and author detail routes throw `notFound` for an id the catalog
  does not hold.
- `RouteStates.tsx` owns the three route states. The not-found page names
  the missing model, provider, or author and links back to its list. The
  error page names the failure and offers a retry that invalidates the
  router.
- Sidebar links read the router's active state with prefix matching and
  set `aria-current` on the active page.
- The files and media-jobs gates read the query factory names instead of
  the old read names.

## Counts

| Item | Before | After |
| --- | --- | --- |
| Read functions that accept a signal | 0 | 31 |
| Route files with a loader | 0 | 15 |
| Detail routes that throw `notFound` | 0 | 3 |
| Console tests | 219 in 34 files | 223 in 35 files |
| Entry chunk gzip | 91.69 kB | 105.21 kB |
| Verifier | 2 passed, 46 failed | 5 passed, 43 failed |

## Entry chunk

The entry chunk grew by 13.5 kB gzip because the loaders import the query
factories and the API module. Before this task those two modules loaded as
separate chunks right after the entry, so the bytes on the first paint did
not change. The invariant I8 bound of 130 kB gzip holds.

## Fail-before

V03, V04, and V05 were red at `401be4e`, as `CPL-B1.md` records. The test
module `router.test.tsx` imports `@/router`, which does not exist at
`401be4e`, so the module does not resolve. The review recorded the bare
"Not Found" text and the exact-path sidebar match as finding LW-07.

## Tests added

`routes/router.test.tsx` proves four facts. An address the router cannot
match names the missing model and links to the list. A model the catalog
does not hold renders the not-found page. The models link stays active on
a model detail route. A model id with a slash round-trips through the
address as one segment.

## Browser check

The dev server on port 5174 rendered the not-found page for
`/models/unknown/unknown` with the models link active. The model detail
route kept the models link active. A hover on the providers link fetched
the providers read and the route component before the click.

## Commands

```bash
pnpm -C console check
bash scripts/verify-console-polish.sh
bash scripts/verify-files-api.sh
bash scripts/verify-async-media-jobs.sh
```
