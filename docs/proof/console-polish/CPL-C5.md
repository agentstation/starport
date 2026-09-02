# CPL-C5 Skeletons, field validation, and live regions

Branch `codex/cpl-c5`, rebased onto main at `e1ccac6` (CPL-C4, PR #339).

## What changed

The loading paragraph is gone from every list, panel, and detail page.
Each site now renders a skeleton that takes the geometry of what it
stands in for. The primitives live in `console/src/components/ui/skeleton.tsx`.

| Shape | Stands in for | Sites |
| --- | --- | --- |
| `TableSkeleton` | A dense table with a header row | 13 lists and the router pending state |
| `CardGridSkeleton` | The two-column card grid | Authors and providers |
| `DetailSkeleton` | A detail page header and first card | Model, author, and provider pages |
| `CardSkeleton` | One overview card | Catalog and providers cards |
| `StatSkeleton` | The 24 h stats row | Overview stats |
| `Skeleton` | Inline rows and the video frame | Model picker, credentials, templates, jobs |

The plan named three shapes. The card grid and the detail page did not
fit any of them. Two more shapes own those geometries, so five routes
do not restate them.

`LoadingStatus` wraps each shaped skeleton in one polite status with the
text "Loading" and `aria-busy`. The bars inside are `aria-hidden`. The
shimmer is a `--animate-shimmer` keyframe in `tokens.css` on the raised
ground, and the reduced-motion block collapses it with every other
animation. The `motion-reduce:animate-none` class on the bar does the
same where the block does not apply.

`Field` in `Form.tsx` gained `error`, `required`, and description
wiring. It clones its control with `aria-describedby` pointing at the
error and the hint. The control gets `aria-invalid` when the field has
an error and `aria-required` when the field needs a value. The error and the hint sit
outside the label element, so the control's accessible name stays the
label. Text controls turn their border to the error color under
`aria-invalid`.

`AssistantMessage` in `Messages.tsx` carries a polite live region. It is
empty while a turn streams and names the model once the turn ends, so a
screen reader hears one sentence per answer.

`LoadFailed` in `console/src/components/ui/LoadFailed.tsx` is the panel a
failed read shows. It names the missing data, quotes the failure, and
offers a retry. The three overview cards render it in place of `null`.
The provider page renders it in place of the served-credential panel
when the activity read fails. The health panel then says "Activity
unavailable" instead of "No requests".

The `identityProviders` read now
throws on a failed request. The `IdentitySignIn` section renders
`LoadFailed` with a retry instead of silence.

## Counts

| Measure | Before | After |
| --- | --- | --- |
| Loading paragraphs in `console/src/routes` | 15 | 0 |
| Loading paragraphs in `console/src/components` | 8 | 0 |
| Overview cards that return `null` while loading | 3 | 0 |
| Console tests | 236 in 39 files | 242 in 41 files |
| Entry chunk gzip | 117.67 kB | 118.47 kB |
| Verifier | 19 passed, 29 failed | 21 passed, 27 failed |

## Fail-before

The six new tests fail at `e1ccac6` in a clean worktree with the same
test files, with 6 failed and 6 passed. The baseline Field has no error
prop. The live region does not exist. The catalog card renders nothing
while loading. The sign-in section stays silent on a failed read. V19 to
V21 were red at that commit, as the C4 proof records.

## Tests added

| File | Test |
| --- | --- |
| `ui/Form.test.tsx` | A field with an error marks its control invalid and links the message |
| `ui/Form.test.tsx` | A field without an error leaves the control valid |
| `chat/Messages.test.tsx` | A finished turn announces itself once through the live region |
| `overview/CatalogCard.test.tsx` | The card holds its shape while the read runs |
| `overview/CatalogCard.test.tsx` | A failed read names the failure and a retry runs the read again |
| `auth/IdentitySignIn.test.tsx` | A failed provider read shows a failure with a retry, not silence |

The first-contact refusal test stubs the provider list as an unconfigured
gateway answers it, because the identity read no longer swallows a
refusal. The usage list-state test asserts no busy region instead of a
loading paragraph.

## Browser reading

In the dev console a bar with the skeleton classes runs the shimmer
animation at 1.8 s. Its background is a 90 degree gradient with the
hover color at 50 percent over the raised ground. The background size
is 200 percent.

## Commands

| Command | Result |
| --- | --- |
| `pnpm -C console check` | Build, typecheck, 242 tests in 41 files pass |
| `grep -rE "Loading [a-z]" console/src/routes` | No matches |
| `bash scripts/verify-console-polish.sh` | 21 passed, 27 failed |
| Every other `scripts/verify-*.sh` gate | Green |
