# CPL-B3 List state in search params and effect hygiene

Seven more routes keep their list state in the address. The usage list
keeps its rows through a filter change. Two effects declare what they read.
The verifier reports V06 green. The reading below dates from
2026-09-01 on branch `codex/cpl-b3` from `0016f74`.

## What changed

- `lib/search.ts` owns the two readers every schema uses. A non-empty
  string counts, and a value outside a named set reads as absent.
- The teams, members, accounts, keys, and presets routes read the open
  record from the address. The accounts route also reads the open panel.
- The providers route reads its search text and sort order from the
  address. The authors route reads its search text.
- The usage route reads the open request from the address. A record
  without a request id falls back to its timestamp.
- Every selection writes through `navigate` with `replace`, so Back does
  not walk through each panel a reader opened.
- The usage activity query sets `placeholderData` to keep the previous
  rows while a filter change loads. The auto-pager skips placeholder pages.
- The usage auto-pager effect and the models debounce effect declare the
  facts they read. The usage draft effect drops its lint suppression.
- `test/console.tsx` owns the route test harness. The router tests and
  the new list-state tests share it.

## Counts

| Item | Before | After |
| --- | --- | --- |
| Route files with `validateSearch` | 4 | 11 |
| Routes that hold a selection in `useState` | 7 | 0 |
| Effects without a dependency list | 1 | 0 |
| Console tests | 223 in 35 files | 225 in 36 files |
| Entry chunk gzip | 105.21 kB | 105.41 kB |
| Verifier | 5 passed, 43 failed | 6 passed, 42 failed |

## Fail-before

V06 was red at `0016f74`, as `CPL-B2.md` records. The test module
`listState.test.tsx` imports `@/test/console`, which does not exist at
`0016f74`. With the harness in place, the keys test finds no editor at
`0016f74` because the route ignores the address. The usage test finds
"Loading requests…" in place of the rows after the filter change.

## Tests added

`routes/listState.test.tsx` proves two facts. An address with a selected
key opens that key's editor. The previous usage rows stay visible while a
filter change loads, and the filtered page replaces them when it lands.

## Commands

```bash
pnpm -C console check
bash scripts/verify-console-polish.sh
grep -l validateSearch console/src/routes/*.tsx | grep -vc '\.test\.'
```
