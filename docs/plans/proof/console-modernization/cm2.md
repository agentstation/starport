# CM2 proof: design tokens, fonts, app shell

Date: 2026-08-21. Branch: `codex/cm-2-tokens-shell` on main @ 7f3c45f.

## What landed

- `console/src/styles/tokens.css`: the executable form of DESIGN.md.
  Role tokens on `:root` (dark default) with a `.light` override block,
  a Tailwind `@theme inline` map that resets the stock palette and
  exposes only role utilities (`bg-bg-canvas`, `text-text-3`,
  `border-border-1`, semantic colors and tints), the full type scale
  (12–32px with paired line heights and tracking), the radius scale,
  `--ease-standard`, the two-layer accent focus ring, and a
  reduced-motion kill switch.
- Self-hosted Geist and Geist Mono (400/500/600 woff2 from the
  `geist` 1.7.2 npm package; OFL 1.1 license committed at
  `console/src/fonts/LICENSE.txt`). Fonts and the favicon live under
  `src/` so Vite hashes them into `/assets/*`. That keeps the CM1 Go
  handler unchanged: every runtime file is served through the
  `/assets/*` immutable route, and nothing needs serving from the dist
  root.
- `console/src/lib/theme.ts`: theme bootstrap. Saved choice wins,
  `system` follows `prefers-color-scheme`, `initTheme()` runs before
  first render in `main.tsx`, and a `matchMedia` listener tracks OS
  changes while on `system`.
- `console/src/components/shell/Shell.tsx`: fixed left sidebar (240px,
  collapses to a 64px icon rail, state persisted in localStorage),
  star wordmark, nav where only Overview is a live route (the other
  seven entries render dimmed until their tasks land), active state =
  `--bg-hover` + 2px accent left bar + Text 1, footer with a gateway
  health dot polling `/health/ready` (30s), version, theme toggle,
  GitHub link, and collapse control. Content column: 1280px max,
  32px gutters.
- `console/src/components/shell/PageHeader.tsx`: 20px/600 title,
  Text 3 description, one optional action slot.
- Lucide 1.x removed brand icons (`Github` is gone), so the GitHub
  mark is an inline octicon path.

## Evidence

- `pnpm -C console check` → Vite build clean (1943 modules; fonts,
  favicon, CSS, JS all hashed into `dist/assets/`), `tsc --noEmit`
  clean.
- `bash scripts/verify-console-modernization.sh` → `Summary: 8
  passed, 13 failed`; CM-V03 (tokens in `@theme`), CM-V05
  (`--color-bg-raised`), CM-V06 (Shell.tsx) newly pass. Fail-before
  for all three recorded in cm0.md.
- Live smoke (`STARPORT_CONSOLE_NEXT=1`, embedded build): `GET /` →
  200; `GET /assets/Geist-Regular-*.woff2` → 200 with
  `public, max-age=31536000, immutable` and the same-origin CSP.
- Rendered checks (Chrome): dark shell (`cm2-shell-dark.jpg`), light
  shell (`cm2-shell-light.jpg`), collapsed rail
  (`cm2-shell-rail.jpg`). After a reload, localStorage restored
  `theme=light` + `collapsed=1` before paint (documentElement carried
  `.light`, sidebar measured 64px) and the computed body font was
  Geist.
- `go test ./...` ok, `go vet` clean, `make lint` 0 issues,
  `make build` complete. Gates: starmap-ownership, v1-architecture,
  dependency-direction (+ self-test), catalog-driven, package-layout,
  readme-quickstart PASS; parity 16 passed; SDK smoke PASS (both).
