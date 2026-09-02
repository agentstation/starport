# CPL-C2 Dialogs and sheets replace Modal and SidePanel

Every console dialog and side panel now renders through the shadcn dialog
and sheet on Base UI. Each one traps focus, restores focus on close, locks
body scroll, marks the page behind it inert, and names itself from its
title. Mutation errors render inside the dialog. Delete actions use a
destructive button. The verifier reports V11 to V13 green. The reading
below dates from 2026-09-01 on branch `codex/cpl-c2` from `ac66a2f`.

## What changed

- `components/ui/dialog.tsx` follows DESIGN.md. It uses radius 12, the
  raised ground, and the level-two border. It uses the overlay shadow
  with its inset ring and a backdrop of black at 0.6. It gains
  `DialogBody` and `DialogError`. The error slot renders as an alert
  above the footer. It renders nothing when empty.
- `components/ui/sheet.tsx` follows the same rules. It slides in from
  the right at 480 px and gains `SheetBody`.
- Both use Base UI state attributes for motion, and both honor
  `prefers-reduced-motion` through the Tailwind `motion-reduce` variant.
- `tokens.css` gains `--error-ink`, its `--color-error-ink` bridge, and
  `--shadow-overlay`.
- `components/ui/Form.tsx` gains `DestructiveButton`, an error fill with
  error-ink text.
- Seventeen dialog sites and seven sheet sites consume the primitives.
  They sit in the keys, presets, credentials, files, teams, accounts,
  members, models, chat, settings, and usage areas. `Modal.tsx` and
  `SidePanel.tsx` are gone.
- Six delete sites use `DestructiveButton`.
- Eleven dialogs carry a `DialogError` slot. Eight dialogs keep their
  own error text and stay open on failure. The parent `onError` props
  are gone.

| Area | Dialogs that keep their error |
| --- | --- |
| Keys | create, edit, delete |
| Presets | history, delete |
| Shared credentials | removal, access |
| Files | delete |

## Counts

| Item | Before | After |
| --- | --- | --- |
| Files that import `Modal` or `SidePanel` | 16 | 0 |
| Hand-rolled `bg-error` buttons | 6 | 0 |
| Dialog sites with an error slot | 0 | 11 |
| Literal `role="dialog"` outside `components/ui` | 3 | 3 |
| Console tests | 227 in 37 files | 231 in 38 files |
| Entry chunk gzip | 105.63 kB | 105.64 kB |
| Verifier | 12 passed, 36 failed | 15 passed, 33 failed |

The three literal dialog roles are the model picker, the composer
attachment popover, and the command palette. CPL-C4 owns them.

## Fail-before

V11 to V13 were red at `ac66a2f`, as `CPL-C1.md` records. The four new
tests fail at `ac66a2f`.

The delete dialog carries no `aria-labelledby`. The body scrolls. Focus
leaves the dialog. Focus does not return to the trigger. The failed
delete renders its error outside the dialog.

## Tests added

`routes/keyDialog.test.tsx` opens the delete key dialog from the row
action and proves four contracts:

- The dialog carries `aria-labelledby` that resolves to its title, and
  the body is scroll locked.
- Focus that leaves past the last control returns inside the dialog.
- Closing the dialog returns focus to the row action that opened it.
- A failed delete renders an alert inside the dialog, and the dialog
  stays open.

## Commands

```bash
pnpm -C console check
bash scripts/verify-console-polish.sh
grep -rn "ui/Modal\|ui/SidePanel" console/src | wc -l
```
