# CPL-C1 Adopt shadcn on Base UI, theme bootstrap, skip link

The console holds a shadcn manifest on Base UI, its eight first primitives,
and no generated palette. A light reader sees no dark flash. A keyboard
reader's first Tab lands on a skip link. The verifier reports V07 to V10
green. The reading below dates from 2026-09-01 on branch `codex/cpl-c1`
from `3b3be33`.

## What changed

- `components.json` records the `base-nova` style with CSS variables, the
  `@` alias, and `src/styles/app.css` as the stylesheet.
- `components/ui` gains button, command, dialog, dropdown-menu, input,
  input-group, popover, sheet, skeleton, tabs, textarea, and tooltip from
  the registry. `lib/utils.ts` owns `cn`.
- The generated palette, font import, and base layer are gone. The bridge
  in `tokens.css` maps every shadcn color name to a role token.
- shadcn's `accent` names a hover ground, not our amber accent. The
  primitives say `bg-bg-hover` and `text-text-1` in its place. The button
  drops its `oklch` literal.
- `index.html` sets `data-theme` before the first paint. `lib/theme.ts`
  and the stylesheets read the attribute in place of the `.light` class.
- The shell renders a skip link to the `main` landmark, which now carries
  `id="main"`.
- The input ring reset in `tokens.css` is gone. Text fields turn their
  border accent under `focus-visible`, beside the two-layer ring.
- The mono logo style paints each mark through a `currentColor` mask. The
  black and white filter is gone.

## Counts

| Item | Before | After |
| --- | --- | --- |
| shadcn primitives in `components/ui` | 0 | 12 |
| Color literals in `components/ui` | 0 | 0 |
| Files with a `focus:` border class | 4 | 0 |
| Console tests | 225 in 36 files | 227 in 37 files |
| Entry chunk gzip | 105.41 kB | 105.63 kB |
| Verifier | 6 passed, 42 failed | 12 passed, 36 failed |

The primitives are not imported yet, so the entry chunk holds none of
them. CPL-C2 through CPL-C5 wire them in.

## Dependencies

`@base-ui/react`, `class-variance-authority`, `clsx`, `cmdk`, and
`tailwind-merge` ship in the bundle. `shadcn` and `tw-animate-css` are
development dependencies. The first pins the CLI and supplies the
`data-*` state variants. The second supplies the enter and exit
animation utilities. Both reach the build as CSS only.

## Fail-before

V07 to V10 were red at `3b3be33`, as `CPL-B3.md` records. The skip-link
test finds no link at `3b3be33`. The mask test finds an inlined SVG under
the mono style, not a mask.

## Tests added

`shell/Shell.test.tsx` proves the skip link is the first focusable
element and targets the `main` landmark. `EntityLogo.test.tsx` proves
the mono style paints a full-color mark through a mask.

## Browser check

The check ran on port 5174 against the dev gateway. The `html` element
carries `data-theme` on first paint. The first Tab lands on "Skip to
content". A mouse click into a text field shows the accent border and
ring. A mono mark renders as a mask.

## Commands

```bash
pnpm -C console check
bash scripts/verify-console-polish.sh
grep -rE "oklch|#[0-9a-f]{6}" console/src/components/ui | wc -l
```
