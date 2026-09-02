# CPL-C3 Toasts and mutations replace notice state

Every write outcome in the console now reaches the reader through one
toaster. The fourteen components that held their own notice state and
timers hold none. Every write site runs through `useMutation`. `ApiError`
names a budget refusal and a guardrail refusal. The copy button shows
"Copied" for two seconds and reports a failed copy. The verifier reports
V14 to V16 green. The reading below dates from 2026-09-01 on branch
`codex/cpl-c3` from `e2b20f7`.

## What changed

- `components/ui/sonner.tsx` wraps sonner with the DESIGN.md toast style.
  Toasts sit bottom right, last four seconds, and hold one line. A
  semantic left rule and an icon name the outcome. The wrapper reads the
  applied theme from the document, so a toast matches the page.
- `components/shell/Shell.tsx` mounts the toaster once, after the
  command palette.
- `lib/mutations.ts` owns the outcome vocabulary. The `announce` helper
  raises a success toast, and the `report` helper raises an error toast.
  The `settled` helper builds an `onSettled` callback from a success line
  and a failure prefix. The `errorText` helper reads a message from an
  unknown error.
- `lib/api.ts` gives `ApiError` two readers. `budgetExhausted` reports
  a 402. `guardrailRefusal` reads the `guardrail_refusal` error type.
- `components/ui/CopyButton.tsx` shows "Copied" in a status region for
  two seconds and reports a failed copy with the manual fallback.
- Fourteen components drop their notice state, timer, and `say` helper.
  Each outcome now calls `announce` or `report`.
- Four hand-rolled writes now use `useMutation`. The catalog refresh
  and the provider refresh read `isPending` for the spinner. The
  credential apply flow keeps its apply error in the dialog and returns
  the validation result as its outcome. The shared credential access
  dialog keeps its error in the dialog.
- `scripts/verify-files-api.sh` reads the refusal toast copy instead of
  the removed notice test id.

## Where an outcome renders

| Surface | Success | Failure |
| --- | --- | --- |
| Dialog | Toast after close | `DialogError` inside the dialog |
| Sheet and panel | Toast | Toast |
| Refresh control | Toast | Toast |
| Client-side validation | Not applicable | Inline text beside the control |

The team budget form keeps its validation message inline. A toast would
leave the reader without the message beside the field that needs the
correction.

## Counts

| Item | Before | After |
| --- | --- | --- |
| `setNotice(` calls in `console/src` | 43 | 0 |
| Files with notice state | 14 | 0 |
| Hand-rolled write handlers | 4 | 0 |
| Toaster mounts | 0 | 1 |
| Console tests | 231 in 38 files | 233 in 38 files |
| Entry chunk gzip | 105.64 kB | 115.81 kB |
| Verifier | 15 passed, 33 failed | 18 passed, 30 failed |

The entry chunk grows by the sonner runtime. Fourteen notice
implementations leave at the same time, so the console gains one
toaster for the cost.

## Fail-before

V14 to V16 were red at `e2b20f7`, as `CPL-C2.md` records. The two new
tests fail at `e2b20f7`. A 402 parses with `budgetExhausted` undefined.
Key creation renders no "Key created" text.

## Tests added

- `lib/api.test.ts` sends a 402 with a permission error body. The
  parsed error reports `budgetExhausted`, keeps the gateway message,
  and does not mark the stored key rejected.
- `routes/keyDialog.test.tsx` opens the create dialog, names a key, and
  creates it. The toast reads "Key created" and the secret dialog shows
  the new secret.
- `components/files/FilesPanel.test.tsx` now mounts the toaster and
  reads the refusal from the toast.

## Commands

```bash
pnpm -C console check
bash scripts/verify-console-polish.sh
bash scripts/verify-files-api.sh
grep -rn "setNotice(" console/src | wc -l
```
