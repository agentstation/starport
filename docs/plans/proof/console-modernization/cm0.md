# CM0 proof: baseline and red verifier

Date: 2026-08-21. Baseline: main @ 2818e74.

## Deliverables

- `DESIGN.md` at the repository root: the Starport design system (seven
  laws, color, typography, space, layout, data display, components, chat,
  voice, accessibility, implementation mapping). Synthesized from three
  web-research reports (stack versions verified against the npm registry
  on 2026-08-21; devtool design exemplars: Linear, Vercel Geist, Stripe,
  Supabase, Resend, OpenRouter; chat composer UX: ChatGPT, Claude
  Desktop, T3 Chat) and a rendered-page review of all eight legacy
  console pages against the live gateway.
- `docs/plans/console-modernization-plan.html`: the durable campaign
  plan, CM0 through CM15.
- `scripts/verify-console-modernization.sh`: the campaign verifier,
  21 conditions.

## Fail-before evidence

`bash scripts/verify-console-modernization.sh` at baseline:

```
FAIL CM-V01 console workspace exists with a package manifest
FAIL CM-V02 package manifest pins pnpm via the packageManager field
FAIL CM-V03 design tokens live in a Tailwind @theme layer
FAIL CM-V04 TanStack Router file routes directory exists
FAIL CM-V05 token layer defines the raised-surface role token
FAIL CM-V06 app shell component exists
FAIL CM-V07 overview route exists
FAIL CM-V08 models route exists
FAIL CM-V09 providers route exists
FAIL CM-V10 keys route exists
FAIL CM-V11 usage route exists
FAIL CM-V12 presets route exists
FAIL CM-V13 settings route exists
FAIL CM-V14 chat route exists
FAIL CM-V15 chat composer carries the model picker
FAIL CM-V16 chat renders streaming markdown through streamdown
FAIL CM-V17 chat has a comparison mode component
FAIL CM-V18 console handler serves the SPA with a fallback
FAIL CM-V19 command palette component exists
PASS CM-V20 DESIGN.md states the one-accent law
FAIL CM-V21 legacy static console is deleted
Summary: 1 passed, 20 failed
```

Exit code 1. The single green condition is the DESIGN.md law guard,
which this task itself introduces. Every build condition is red, so
each later task has a real fail-before state.

## Legacy console inventory (what CM14 deletes)

`internal/console/static/js`: api.js, app.js, freshness.js, markdown.js,
router.js, ui.js, pages/{chat, keys, models, overview, presets,
providers, settings, usage}. `static/css`: chat.css, console.css,
tokens.css. Vendored: marked, KaTeX (+18 fonts), Mermaid, Prism,
DOMPurify, IBM Plex Mono woff2. `templates/index.html`.

## Parity cross-dependency

`scripts/verify-openrouter-parity.sh` conditions ORP-V06, ORP-V09, and
ORP-V16 grep legacy console paths. CM14 re-points them in the cutover
pull request without weakening what they assert.
