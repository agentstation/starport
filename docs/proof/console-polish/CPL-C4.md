# CPL-C4 Popovers, menus, tooltips, command palette, and combobox

Every floating surface in the console now renders through a shadcn
primitive on Base UI or cmdk. The four hand-rolled overlays are gone.
Every icon-only button sets its accessible name through one `IconButton`
or a tooltip trigger, and no button carries a `title` attribute. The
command palette runs on cmdk with a Spotlight-scale panel, a Keys group,
and five persisted recents. The verifier reports V17 and V18 green. The
reading below dates from 2026-09-01 on branch `codex/cpl-c4` from
`4b37043`.

## What changed

- `components/ui/popover.tsx`, `tooltip.tsx`, `dropdown-menu.tsx`,
  `tabs.tsx`, and `command.tsx` carry the DESIGN.md overlay tokens. A
  popover uses the raised background, the second border, radius 8, and
  the overlay shadow. A focused panel keeps that shadow instead of the
  accent ring. Tabs offer a `line` variant with an accent underline and a
  `chips` variant with bordered pills.
- `components/ui/IconButton.tsx` renders a square button inside a
  tooltip. The `label` prop sets the accessible name and the tooltip.
- `components/shell/Shell.tsx` mounts one `TooltipProvider` around the
  shell. The theme and collapse buttons show a tooltip on the right.
- `components/chat/Composer.tsx` drops its local popover. The model
  picker, the reasoning effort menu, and the request parameters now open
  as popovers. The picker closes on Escape and returns focus to the
  textarea.
- `components/chat/ModelPicker.tsx` renders as popover content. Its
  search box is a combobox with `aria-controls` and
  `aria-activedescendant`, and every provider section is a labelled
  group.
- `components/chat/Messages.tsx` renders the retry menu as a dropdown
  menu. The retry buttons and the thread list actions use `IconButton`.
- `components/models/FreshnessBar.tsx` renders the catalog details as a
  popover and the refresh control as an `IconButton`.
- `components/ui/FacetFilter.tsx` renders on the popover primitive. A
  sibling button clears the facet. A search field appears above eight or
  more options, and focus roves through the option list.
- `routes/docs.tsx` and `components/overview/QuickstartCard.tsx` use the
  tabs primitive, so each tab controls one panel.
- `components/palette/CommandPalette.tsx` runs on cmdk inside a dialog.
  The Keys group lists gateway keys. A Recent group lists the last five
  entities from `paletteRecents.ts`.
- `components/ui/command.tsx` re-syncs `aria-activedescendant` after
  cmdk selects the first result. The cmdk scheduler reads the selected
  item before that item renders. The attribute therefore stayed empty
  until the next arrow key.
- `src/test/setup.ts` stubs `ResizeObserver` and `scrollIntoView` for
  jsdom, because cmdk and Base UI call both without a guard.

## Counts

| Item | Before | After |
| --- | --- | --- |
| Hand-rolled overlays | 4 | 0 |
| Buttons with a `title` attribute | 8 | 0 |
| Palette entity kinds | 5 | 6 |
| Console tests | 233 in 38 files | 236 in 39 files |
| Entry chunk gzip | 115.81 kB | 117.67 kB |
| Palette chunk gzip | none | 19.49 kB |
| Verifier | 18 passed, 30 failed | 19 passed, 29 failed |

The entry chunk grows by the Base UI popover, tooltip, menu, and tabs
runtimes. The palette dialog and cmdk load in their own chunk on the
first open, so the entry stays under the I8 limit of 130 kB gzip.
The four overlay implementations and their outside click handlers leave
at the same time.

## Fail-before

V17 and V18 were red at `4b37043`, as `CPL-C3.md` records. The three new
tests fail at `4b37043` in a clean worktree with the same test files.
The details block renders without a dialog role. The palette input has
no combobox role. The picker input has no `aria-activedescendant`.

## Tests added

- `components/models/FreshnessBar.test.tsx` clicks Details, reads the
  generation in the popover, and closes it with Escape.
- `components/palette/CommandPalette.test.tsx` types a full model id,
  finds the option in the Models group, and reads its id from
  `aria-activedescendant`.
- `components/chat/ModelPicker.test.tsx` reads the first option id from
  `aria-activedescendant`, presses ArrowDown, and reads the second.

## Browser reading

Six surfaces render in Chrome against the dev server. They are the
palette, the catalog details popover, the tag facet, the docs tabs, the
model picker, and a composer tooltip. The focused details panel shows
the overlay shadow and no accent ring.

## Commands

```bash
pnpm -C console check
bash scripts/verify-console-polish.sh
bash scripts/verify-console-modernization.sh
```
