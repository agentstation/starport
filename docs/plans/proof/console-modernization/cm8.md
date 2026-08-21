# CM8 proof: presets page

Date: 2026-08-21. Branch: `codex/cm-8-presets` on main @ 973b367.

## What landed

- `console/src/routes/presets.tsx`: the presets page over the
  `/api/v1/presets` CRUD API. The table shows each preset's name as
  an `@preset/name` mono chip, its model target (single model or
  fallback chain count), routing pills (sort, provider order joined
  with arrows, only/ignore lists, price caps as "≤$X/M in|out", "no
  fallbacks"), a sampling-override summary (temperature, top-p, max
  tokens, seed, system prompt, stop count), and a relative updated
  time with a UTC tooltip. Dedicated states cover a missing key, a
  locked `presets:write` scope, an unconfigured preset store (503),
  load errors, and an empty catalog with a curl snippet showing how
  requests select a preset by `"model": "@preset/<name>"`.
- Editor modal for create and edit: name (immutable after create —
  the field disables with a hint on edit), description, model via
  `ModelPicker`, fallback model chain, sampling section
  (temperature, top-p, presence/frequency penalty, max tokens,
  seed, stop sequences, system prompt), and provider routing
  section (order, only, ignore, sort, price caps, allow-fallbacks
  toggle). Empty fields are dropped from the config; an empty
  config is rejected client-side. Updates send the loaded
  `revision`, and a 409 renders "Preset changed elsewhere — reload
  and retry."
- Delete modal restates the `@preset/name` reference in bold mono
  and warns that requests referencing it start failing immediately.
- `console/src/components/models/ModelPicker.tsx`: a combobox over
  the live model catalog (`useQuery(["models"])`, shared cache with
  the models page). Substring-filtered ID suggestions (max 8),
  keyboard navigation, no frontend model facts — every suggestion
  comes from `/models` at runtime.
- `console/src/components/ui/Form.tsx`: shared form scaffolding
  (`INPUT_CLASS`, `SELECT_CLASS`, `TEXTAREA_CLASS`, `Field`,
  `GhostButton`, `PrimaryButton`, `RowAction`) extracted from the
  keys page; `console/src/routes/keys.tsx` now imports it with no
  behavior change.
- `console/src/lib/api.ts`: `Preset`, `PresetConfig`,
  `PresetProviderPreferences`, `PresetWriteRequest`, and
  `listPresets` / `createPreset` / `updatePreset` / `deletePreset`.
- `console/src/components/shell/Shell.tsx`: Presets nav entry
  flipped to implemented.

## Evidence

- `pnpm -C console check` → Vite build + `tsc --noEmit` clean.
  Presets chunk 16.26 kB (5.18 kB gzip), lazy-loaded.
- `bash scripts/verify-console-modernization.sh` → `Summary: 14
  passed, 7 failed`; CM-V12 newly passes. Fail-before recorded in
  cm0.md.
- Live verification (`STARPORT_CONSOLE_NEXT=1`, embedded build, real
  gateway, admin key, real groq credential):
  - Page rendered the pre-existing `@preset/fast-groq` with its
    `sort price` and `order groq` routing pills.
  - Create flow: name `cm8-proof`, description, ModelPicker typed
    "compound" and suggested `groq/compound` and
    `groq/compound-mini` from the live catalog; picked
    `groq/compound-mini`, set temperature 0.2 and sort `price` →
    "Created @preset/cm8-proof" notice and a new row with a
    `temp 0.2` override chip.
  - Edit flow: name field disabled with the "names are immutable"
    hint, values pre-filled, temperature changed to 0.7 → "Preset
    saved" and the chip updated (revision-checked PUT succeeded).
  - End-to-end preset routing: `POST /api/v1/chat/completions` with
    `"model": "@preset/cm8-proof"` → 200, resolved to
    `groq/compound-mini` on provider groq, content "ready", 486
    total tokens.
  - Delete flow: modal restated `@preset/cm8-proof` in bold mono
    with the failing-requests warning; confirm → "Preset deleted"
    notice and only `fast-groq` remained.
  - Keys page re-verified after the Form.tsx extraction: table,
    scope chips, budget bars, and row actions render unchanged.
  - Light theme verified: pills, chips, and modals hold on the
    light ground.
- Rendered proof: `cm8-presets-dark.jpg`, `cm8-presets-light.jpg`
  (both showing the two-row table with `cm8-proof` present).
- Full gate suite on the branch: see the execution log row for CM8.
