# CPL-G1 proof: small screens

Base: the CPL-F5 squash `dee2729`.

## What changed

| Path | Change |
| --- | --- |
| `console/src/lib/useMediaQuery.ts` | New. `useMediaQuery` reads a media query through `useSyncExternalStore`. `SMALL_SCREEN` names the 639 px breakpoint. |
| `console/src/components/ui/sheet.tsx` | `SheetContent` gains a `side` prop: `right`, `left`, or `bottom`. |
| `console/src/components/shell/SmallScreenNav.tsx` | New. The top bar below the breakpoint: a navigation sheet trigger, the brand, and a search button. |
| `console/src/components/shell/Shell.tsx` | `SidebarBody` owns the sidebar content. The shell renders it in the aside on desktop and in a lazy left sheet on a small screen. |
| `console/src/components/chat/Composer.tsx` | The model picker opens as a bottom sheet on a small screen and as a popover elsewhere. |
| `console/src/routes/chat.tsx` | The thread list opens as a left sheet on a small screen, closed until asked for. |
| `console/src/components/ui/Form.tsx` | `RowAction` is 44 px tall below `sm`. |
| `console/src/components/ui/IconButton.tsx` | Every icon button has a 44 px minimum box below `sm`. |
| `console/src/components/chat/Composer.tsx` | `BarButton` is 44 px tall below `sm`. |
| `console/src/routes/index.tsx`, `authors.tsx`, `providers.tsx`, `usage.tsx`, `skeleton.tsx` | Two-column grids declare `grid-cols-1` below their breakpoint. |
| `console/src/routes/usage.tsx` | The usage table scrolls inside its box below `sm` and declares its column sum as a minimum width. |
| `console/src/routes/authors_.$authorId.tsx` | The author model table scrolls inside its box below `sm`. |
| `console/src/routes/smallScreen.test.tsx` | New. Three tests: the sheet trigger at 375 px, the aside on desktop, and the picker sheet. |

## Design notes

The stylesheet handles most of the breakpoint. The hook exists for the three places where the component tree differs: the sidebar, the thread list, and the picker. Each mounts a sheet on one side of the breakpoint. The other side mounts a popover or an aside.

The top bar is a lazy chunk. A desktop first paint does not download the sheet machinery, so the main chunk keeps its previous size. The shell passes the sidebar body and the brand to the chunk as elements. The chunk therefore has no import back into the shell.

The horizontal overflow at 375 px had one root cause on four pages. A `grid gap-4 lg:grid-cols-2` container has an implicit `auto` track below its breakpoint. An `auto` track grows to the widest child, so a card with a long code line or a clamped paragraph pushed the page wide. The `grid-cols-1` class gives the track a `minmax(0, 1fr)` size and the cards clip and wrap inside it.

The usage table keeps its window-sticky header on desktop. Below `sm` the table scrolls inside its own box instead, and its header no longer sticks. The author model table follows the same rule.

## Counts

| Check | Before | After |
| --- | --- | --- |
| Console tests | 402 | 405 |
| Console test files | 64 | 65 |
| Main chunk gzip | 119.17 kB | 119.74 kB |
| Lazy chunks | none for the shell | `SmallScreenNav` 0.57 kB, `sheet` 1.44 kB |
| Verifier | 45 passed, 3 failed | 46 passed, 2 failed |
| Routes wider than the 375 px viewport | 4 of 24 | 0 of 24 |

## Fail-before

At `dee2729` with `smallScreen.test.tsx`, `useMediaQuery.ts`, and `SmallScreenNav.tsx` copied in, the suite reports 2 failed and 1 passed. The trigger test fails because no button named "Open navigation" exists. The picker test fails because the dialog slot is `popover-content`, not `sheet-content`. The desktop test passes at baseline, because the aside already renders there.

## Commands

```
pnpm typecheck
./node_modules/.bin/vitest run
pnpm build
bash scripts/verify-console-polish.sh
```

The verifier prints `Summary: 46 passed, 2 failed`. The remaining red checks are V47 and V48, which CPL-Z1 owns.

Repository gates: 23 of 23 pass.

## Visual check

The browser resize tool leaves the viewport at 1440 px. The walkthrough therefore runs inside a same-origin frame of 375 by 812 px on the dev console. A script loads each route in the frame and compares the document scroll width with the frame width. Before the grid change, the overview, authors, usage, and providers routes were wider than the frame. After it, all 24 routes match the frame width: the 21 file routes plus an author, a provider, and a model detail.

At 375 px the docs page shows the top bar with the navigation trigger, the brand, and the search button. The trigger opens a left sheet with the search row, every section, and the footer. The chat page opens the picker as a bottom sheet with the search field and the grouped list. The composer buttons are 44 px tall.
