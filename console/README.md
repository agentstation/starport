# Starport console

The console is the single-page application the gateway serves at `/`. The Go binary embeds the production build from `internal/console/dist`.

## Stack

| Concern | Choice |
| --- | --- |
| Framework | React 19 with TanStack Router and TanStack Query |
| Components | shadcn on Base UI primitives, restyled through the tokens in `DESIGN.md` |
| Styling | Tailwind v4 with `@theme` tokens in one CSS file; classes compose through `cn` |
| Lint | `@shadcn/lint` under oxlint, configured in `.oxlintrc.json` at error level |
| Charts | Recharts through the shadcn chart wrapper |
| Tests | Vitest with Testing Library in jsdom |

## Commands

```
pnpm dev --host 127.0.0.1 --port 5174
pnpm lint
pnpm typecheck
pnpm test
pnpm build
pnpm check
```

The dev server proxies gateway calls to `localhost:8080`. Start the gateway with `starport dev` and open the launch link it prints. The build writes to `internal/console/dist`, and `pnpm check` runs the lint, the build, the typecheck, and the tests in that order.

## Test conventions

Route tests render the router with the shell through `openConsole` from `src/test/console.ts`. The `stubGateway` helper answers gateway paths from a map and installs an in-memory `localStorage` and a `matchMedia` stub. Call `resetGateway` after each test. The suite has no jest-dom matchers, so assertions read attributes and text directly.

## Design evidence

`scripts/verify-console-polish.sh` at the repository root checks the shipped console against the design rules. Run it before a pull request that changes the console. The design rules live in `DESIGN.md`.

`pnpm lint` enforces the component ownership rule from `DESIGN.md`: a component under `src/components/ui` owns its color, typography, spacing, shape, and motion, and a call site adds layout alone. A dynamic value reaches the page as a CSS custom property that a static utility reads, never as an inline style. A lint message names the variant or size to use and points at `DESIGN.md` and the shadcn skill.

## Agent skills

The shadcn skill lives at `.agents/skills/shadcn` in the repository root, pinned by `skills-lock.json`, with `.claude/skills/shadcn` a symlink to it. Every agent that edits the console reads the same rules. `npx skills update` refreshes it. The two PNG icons under its `assets` directory are ignored by git because the review gate refuses a binary diff; the skill reads the same without them.
