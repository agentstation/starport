# CPL-F1 proof: shell and navigation polish

Branch `codex/cpl-f1`. Base: the CPL-E7 squash.

## What changed

| Owner | Change |
| --- | --- |
| `console/src/components/shell/Shell.tsx` | The rail reads "API keys" and "Audit log" in sentence case. The collapse toggle carries `aria-expanded` and names the rail through `aria-controls`. A click on rail whitespace no longer flips the rail. |
| `console/src/components/palette/PaletteDialog.tsx` | The page destinations gain "Audit log" and read "API keys". |
| `console/src/components/chat/Composer.tsx` | The send button reads `text-accent-ink` on the accent fill, so the arrow keeps its contrast in both themes. |
| `console/src/routes/keys.tsx` | The page heading reads "API keys", the same words as the rail. |
| `console/src/routes/chat.tsx` | The budget refusal names "the API keys page" with the same words as the rail. |
| `scripts/verify-catalog-performance.sh` | CPV17 expected the title case label. It now expects "API keys", so the two gates agree on one label. |

## Design notes

Every other destination in the rail was already sentence case. The two title case labels read as a different voice, so the rename removes the odd ones out rather than a style rule.

The whitespace toggle had no visible affordance. A reader who clicked between two links to dismiss a tooltip saw the rail collapse with no control named. The footer toggle is the one control now, and its state reads through `aria-expanded`.

## Counts

| Measure | Before | After |
| --- | --- | --- |
| Console tests | 370 | 372 |
| Entry chunk, gzip | 118.80 kB | 118.81 kB |
| Verifier | 40 passed, 8 failed | 41 passed, 7 failed |

## Fail-before

I ran the check at `origin/main` (`bf189d4`, the CPL-E6 squash) with the new shell tests copied in. V41 was red, and the verifier reported 39 passed, 9 failed. Both new tests in `console/src/components/shell/Shell.test.tsx` failed there. The rail read "API Keys" and "Audit Log", and the toggle carried no `aria-controls`.

## Tests added

| File | Test |
| --- | --- |
| `console/src/components/shell/Shell.test.tsx` | "the navigation labels read in sentence case" asserts the two links by their new names and the absence of the old ones. "the collapse toggle exposes the rail state and whitespace does not toggle it" asserts `aria-controls` names the rail, a whitespace click leaves `aria-expanded` true, and the toggle flips it to false. |

## Commands

| Command | Result |
| --- | --- |
| `pnpm typecheck` | Clean. |
| `pnpm exec vitest run` | 57 files, 372 tests passed. |
| `pnpm build` | Built. Entry chunk 118.81 kB gzip. |
| `bash scripts/verify-console-polish.sh` | 41 passed, 7 failed. V41 passes. |
| `bash scripts/verify-*.sh` (23 gates, release build gates excluded) | 22 passed in the loop. `verify-catalog-performance.sh` failed on CPV17 before the label update. It passes after the update, 20 of 20. |

## Visual check

I opened the console on the dev gateway with the vite server on port 5174. The rail lists "API keys" under Account and "Audit log" under Gateway. The collapse toggle names `console-sidebar` through `aria-controls` and reports `aria-expanded` true. A synthetic click on the rail itself left the rail at its full width and the toggle at true. A click on the toggle narrowed the rail and flipped the toggle to false with the label "Expand sidebar". A second click restored it.

The command palette answers the query "audit" with the "Audit log" page as its first result. On the chat page, the send button renders the arrow in dark ink on the amber accent fill in the dark theme.

UNVERIFIED: the send button in the light theme. The token file sets the light `--accent-ink` to white on the same accent. The class reads the token in both themes, so the light theme evidence is the class name alone.
