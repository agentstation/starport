# CM1 proof: frontend workspace scaffold and embed

Date: 2026-08-21. Branch: `codex/cm-1-console-scaffold` on main @ cca2b3e.

## What landed

- `console/` workspace: Vite 8.2.2, React 19.2.8, TypeScript 7.0.2,
  Tailwind 4.3.3 (via `@tailwindcss/vite`), TanStack Router 1.170.31
  with `@tanstack/router-plugin` 1.168.34 and React Query 5.101.4.
  pnpm 11.22.0 pinned through the `packageManager` field;
  `pnpm-lock.yaml` committed. The build lands in
  `internal/console/dist` so `go:embed` can reach it, and restores
  `dist/.gitkeep` so the directive compiles without a frontend build.
- Version correction against the plan: `@tanstack/router-plugin`
  versions independently of the router; its latest is 1.168.34 and the
  planned 1.170.31 pin does not exist on the registry.
- TypeScript 7.0.2 works as the type checker after removing `baseUrl`
  (removed in TS 7; `paths` now requires relative entries). No 6.0.x
  fallback needed.
- `internal/console/spa.go`: `SPAHandler` embeds `all:dist`, serves the
  SPA index for every page path with `Cache-Control: no-cache` and a
  same-origin CSP, serves `/assets/*` with
  `public, max-age=31536000, immutable`, rejects traversal, and serves
  a 503 "console not built" notice when the embed holds only
  `.gitkeep`.
- `console.PageServer` interface seam; the server and controllers
  depend on it instead of the concrete legacy handler. `openConsole`
  picks the SPA when `STARPORT_CONSOLE_NEXT=1`; the legacy console
  stays the default.
- `make build` now depends on `console-build`, which builds the
  console when pnpm exists and degrades with a notice otherwise. The
  CI Build job checks the workspace (`pnpm -C console check`) using
  the runner's Node, adding no new pinned actions.

## Evidence

- `bash scripts/verify-console-modernization.sh` → `Summary: 5 passed,
  16 failed`; CM-V01, CM-V02, CM-V04, CM-V18, CM-V20 pass (fail-before
  for all but CM-V20 recorded in cm0.md).
- `go test ./internal/console/...` → ok. New tests:
  `TestSPAHandlerServesIndexForEveryPagePath`,
  `TestSPAHandlerServesHashedAssetsImmutable`,
  `TestSPAHandlerRejectsMissingAndTraversalAssets`,
  `TestSPAHandlerWithoutBuildServesNotice`,
  `TestNewSPAHandlerUsesEmbeddedDist`.
- `go test ./...` → all packages ok. `go vet ./...` → clean.
  `make lint` → 0 issues. `make build` → complete.
- Repository gates: starmap-ownership, v1-architecture,
  dependency-direction (+ verifier self-test), catalog-driven,
  package-layout, readme-quickstart all PASS;
  `verify-openrouter-parity.sh` → 16 passed, 0 failed;
  `smoke-openrouter-sdks.sh` → PASS.
- Live smoke, flag on (`STARPORT_CONSOLE_NEXT=1`): `GET /` → 200 SPA
  index, `no-cache`, CSP without nonce; `GET /assets/index-*.js` → 200
  `immutable`; `GET /models` → 200 SPA fallback. Flag off: `GET /` →
  legacy console shell, `GET /static/css/console.css` → 200.

## Verifier amendments

- CM-V07 now requires the overview route to render its page components
  (`components/overview`), because the CM1 placeholder index route
  would have satisfied a bare existence check and destroyed CM3's
  fail-before state.
- CM-V18 now greps `go:embed all:dist` in `internal/console/spa.go`;
  the original condition named handler.go, where the SPA contract does
  not live.
