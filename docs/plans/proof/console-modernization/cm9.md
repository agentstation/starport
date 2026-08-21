# CM9 proof: settings page

Date: 2026-08-21. Branch: `codex/cm-9-settings` on main @ 079fb98.

## What landed

- `console/src/routes/settings.tsx`: calm-density settings page.
  Sequential flat sections with hairline dividers per DESIGN.md —
  no card grid.
  - **Connection**: masked key input with a reveal toggle and a
    save-and-test flow — the draft key is stored, `/models` is
    fetched as the probe, and on failure the previous key is
    restored with "Key rejected: <gateway message>". Success shows
    "Key valid · N models visible" and invalidates every query so
    locked pages unlock immediately. The stored key renders as a
    head/tail-truncated mono chip; Clear removes it.
  - **Appearance**: a three-way radio group (Dark / Light /
    System). System follows `prefers-color-scheme`, and the saved
    choice drives the whole app.
  - **Chat data**: local conversation count, JSON export via a
    blob download, and delete-all behind a destructive
    confirmation modal that restates the count in bold with "There
    is no undo." Reads the same `starport.chats` store the chat
    page uses.
  - **About**: gateway origin plus version and storage from
    `/api/v1/admin/info`; the section degrades to the origin alone
    when the endpoint is locked. An "unavailable" uptime is
    suppressed.
- `console/src/lib/theme.ts`: theme-change listeners
  (`onThemeChange`) so subscribers re-render when the choice
  changes from anywhere, including OS-level changes while on
  System.
- `console/src/components/shell/Shell.tsx`: the sidebar
  `ThemeToggle` now subscribes via `useSyncExternalStore`, so it
  stays in sync with the settings radio group; Settings nav entry
  flipped to implemented.

## Evidence

- `pnpm -C console check` → Vite build + `tsc --noEmit` clean.
- `bash scripts/verify-console-modernization.sh` → `Summary: 15
  passed, 6 failed`; CM-V13 newly passes. Fail-before recorded in
  cm0.md.
- Live verification (`STARPORT_CONSOLE_NEXT=1`, embedded build, real
  gateway):
  - Invalid key save-and-test → "Key rejected: Invalid API key" in
    the error tone and the previous key restored (pages stayed
    unlocked).
  - Valid admin key save-and-test (Enter submits) → "Key valid ·
    422 models visible" in the success tone, input cleared.
  - Theme radio: System selected → radio moved, page kept the
    OS-resolved theme; Light selected → light palette applied and
    the sidebar toggle label flipped to "Dark theme" in the same
    render (listener sync verified).
  - Delete-all modal restated "8 conversations" in bold with the
    no-undo warning; cancelled without deleting (real user data).
  - Export JSON → `starport-chats.json` (4,530 bytes) downloaded.
  - About rendered gateway origin, version 1.0.0, storage badger;
    the unavailable uptime row is suppressed.
- Rendered proof: `cm9-settings-dark.jpg`, `cm9-settings-light.jpg`.
- Full gate suite on the branch: see the execution log row for CM9.
