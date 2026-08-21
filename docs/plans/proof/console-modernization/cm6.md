# CM6 proof: keys page

Date: 2026-08-21. Branch: `codex/cm-6-keys` on main @ 0f3812a.

## What landed

- `console/src/routes/keys.tsx`: full key management from the admin
  API. Dense table (name, head-tail truncated ID chip with copy and
  full-ID tooltip, scope pills, limits chips, lifecycle pill,
  relative created time with UTC tooltip). Budgeted keys fetch their
  detail record and render thin spend/token bars with "$X left" /
  "N tok left" (warning above 80%, error at exhaustion).
- Create modal: name, admin-scope checkbox (default scopes are the
  non-admin set the legacy console used), allowed models, expiry
  date (inclusive day), request/spend/token limits. Spend entered in
  USD and converted to nano-USD. The secret renders exactly once in
  a dedicated modal with copy; the table holds only IDs after that.
- Edit modal reuses the same limits fields; clearing a field clears
  the restriction (empty `allowed_models` list, `limits: {}`).
  Expiry cannot be removed once set (backend contract). Disable/
  enable toggles `active` and flips the pill to the neutral disabled
  tint. Delete modal restates the key name in bold and warns that
  apps lose access immediately.
- BYOK modal per key: lists attached provider credentials with
  last-used and validate/detach actions. The attach form is
  catalog-driven from `credential_fields` (secret-kind fields go to
  `credentials`, others to `config`). Provider keys are self-managed
  by backend contract (`RequireKeyOwnership`): for any key other
  than the one authenticating the session the modal explains
  "Provider keys are self-managed: only requests authenticated with
  this key can view or change them." and hides the attach form,
  instead of surfacing a raw 403.
- Empty state: icon, one sentence, "New key" CTA, and a mono curl
  snippet against the page's own origin. The header "New key" button
  renders only when the table is non-empty (one primary per
  viewport).
- `console/src/components/ui/Modal.tsx`: shared dialog surface
  (radius 12, border-2, raised ground, black/60 backdrop, Escape and
  backdrop close).
- `console/src/lib/api.ts`: key/limit/budget/provider-key types and
  the admin keys + per-key provider-keys endpoints; `CredentialField`
  added to the provider catalog entry.
- `console/src/components/shell/Shell.tsx`: Keys nav entry flipped
  to implemented.

## Evidence

- `pnpm -C console check` → Vite build + `tsc --noEmit` clean.
- `bash scripts/verify-console-modernization.sh` → `Summary: 12
  passed, 9 failed`; CM-V10 newly passes. Fail-before recorded in
  cm0.md.
- Live verification (`STARPORT_CONSOLE_NEXT=1`, embedded build, real
  gateway, admin key):
  - Table rendered the real keys: local-admin (scope `*`, no
    limits) and budget-demo ($5.00/day spend + 1,000 tok/day budgets
    with green "left" bars fed by the detail endpoint).
  - Create flow issued a `cm6-proof` key with a $2/day budget; the
    plaintext secret appeared exactly once in the secret modal with
    copy, and the table reloaded with the new row.
  - Disable flipped the pill to neutral "disabled" and the action to
    "enable"; the inline notice reported "Key disabled".
  - BYOK on the session's own key listed three previously attached
    credentials (anthropic, groq, openai); validate on groq returned
    the green "groq key is valid" notice (real credential). BYOK on
    another key rendered the self-managed explanation with the
    attach form hidden.
  - Delete restated "cm6-proof" in the destructive modal; confirming
    removed the row and showed "Key deleted", leaving only
    local-admin and budget-demo.
  - Light theme verified: tints, accent (#b45309), and pills hold.
- Rendered proof: `cm6-keys-dark.jpg`, `cm6-keys-light.jpg`.
- Full gate suite on the branch: see the execution log row for CM6.
