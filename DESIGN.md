# Starport design system

This file is the source of truth for the Starport console's visual and
interaction design. Component code, tokens, and page layouts follow this
document. When a change needs a new pattern, add the pattern here first.

Starport is an operator's console for inference infrastructure. Its atoms are
model IDs, API keys, prices per million tokens, token counts, latencies, and
provider statuses. The design reads as **instrumentation**: calm, dark-first,
almost entirely achromatic, with monospace as the voice of data and exactly
one brand accent.

Reference points: Linear (surfaces, elevation, motion), Vercel Geist (color
role contract, type pairing), Stripe (data discipline), Supabase (border-based
elevation, accent rationing), OpenRouter (information architecture to match
and out-finish).

## Laws

These seven laws resolve every ambiguous decision:

1. **One accent, four jobs.** The beacon amber appears only as: primary CTA,
   link, focus ring, selected/active state. Everything else is neutral or
   semantic.
2. **Mono is the voice of data.** Every machine value — model ID, key, price,
   token count, latency, timestamp, URL — renders in mono, tabular, and
   copyable. Body prose is never mono.
3. **Borders are elevation.** Surfaces stack by luminance plus a 1px hairline.
   Shadows exist only above the page plane (popover, dropdown, modal) and
   always pair with an inset 1px ring.
4. **Role-mapped neutrals.** Components reference roles (canvas, panel,
   raised, hover; border 1–3; text 1–4). Themes swap values; components never
   hard-code a hex.
5. **Weight 600 ceiling; tracking only at 20px and above.**
6. **Dense tables, calm pages.** Density lives inside data surfaces. Page
   chrome stays airy.
7. **Color means state.** A colored pixel is interactive or reporting a
   condition. Decorative color does not exist.

## Color

Tokens are defined in OKLCH (perceptually uniform; light theme derives
cleanly). Hex values below are the resolved sRGB approximations. Dark is the
design-first theme; both themes ship together and both must pass review.

### Neutrals — dark

| Role | Token | Value | Use |
|---|---|---|---|
| Canvas | `--bg-canvas` | `#0a0b0c` | Page ground |
| Panel | `--bg-panel` | `#101112` | Sidebar, section grounds |
| Raised | `--bg-raised` | `#18191b` | Cards, chips, inputs |
| Hover | `--bg-hover` | `#1f2124` | Row hover, item hover |
| Border 1 | `--border-1` | `rgba(255,255,255,0.05)` | Hairline dividers |
| Border 2 | `--border-2` | `rgba(255,255,255,0.08)` | Standard borders |
| Border 3 | `--border-3` | `rgba(255,255,255,0.12)` | Emphasized/hover borders |
| Text 1 | `--text-1` | `#f6f7f8` | Primary text, values |
| Text 2 | `--text-2` | `#c9ced6` | Secondary text, body |
| Text 3 | `--text-3` | `#8b909a` | Metadata, labels, captions |
| Text 4 | `--text-4` | `#62666d` | Disabled only (fails AA; documented) |

### Neutrals — light

Canvas `#ffffff`, panel `#fafafa`, raised `#f4f4f5`, hover `#ececee`.
Borders: alpha-black `0.06 / 0.10 / 0.15`. Text: `#18181b / #3f3f46 /
#71717a / #a1a1aa`. Same roles, same components, no per-component overrides.

### Accent — beacon amber

The Starport star is amber. The accent keeps that identity and is the only
brand color in the product.

| Theme | Fill | Fill hover | On-fill text | Link/text accent |
|---|---|---|---|---|
| Dark | `#f0b23e` | `#f6c35c` | `#1a1204` | `#f0b23e` |
| Light | `#b45309` | `#92400e` | `#ffffff` | `#a16207` |

- Accent fills carry dark ink on dark theme (amber is a light color) and white
  ink on light theme (where the accent deepens to keep AA contrast).
- Jobs (law 1): primary CTA, links, two-layer focus ring
  (`0 0 0 2px accent@40%, 0 0 0 4px accent@20%`), selected states (active
  nav text marker, selected row, active tab underline, chosen model row).
- Never on charts by default, never as decoration, never for status.

### Semantic

Because the accent is amber, **warning shifts to orange** to stay
distinguishable. Each semantic color is a dual token: a solid for text/dots
and a 14%-alpha tint for pill backgrounds.

| State | Solid (dark) | Solid (light) | Use |
|---|---|---|---|
| Success | `#4ade80` | `#15803d` | Healthy, active, ok |
| Warning | `#fb923c` | `#c2410c` | Degraded, near limit, stale |
| Error | `#f87171` | `#b91c1c` | Unavailable, failed, exhausted |
| Info | `#60a5fa` | `#1d4ed8` | Neutral notices |

Pill recipe: tint background, solid text, 9999px radius, 12px/500. Dot
recipe: solid 8px dot + plain text label. **Dots report liveness; pills
report lifecycle.**

## Typography

- **UI sans: Geist Sans** (OFL, self-hosted woff2 in the bundle — the console
  must work air-gapped; no font CDN at runtime). Fallback:
  `system-ui, "Segoe UI", sans-serif`.
- **Mono: Geist Mono**, same family for guaranteed harmony. Fallback:
  `"JetBrains Mono", ui-monospace, monospace`.
- Mono renders one size down from adjacent sans (13px mono beside 14px sans).

### Scale

| Step | Size/line | Weight | Use |
|---|---|---|---|
| xs | 12/16 | 400–500 | Table headers, pills, fine metadata |
| sm | 13/18 | 400 | Mono data, secondary UI |
| base | 14/20 | 400–500 | Default UI text, nav, tables |
| md | 16/24 | 400 | Body prose, form inputs, chat |
| lg | 20/28 | 600 | Page titles, section heads |
| xl | 24/32 | 600 | Stat values, page titles (spacious pages) |
| 2xl | 32/38 | 600 | Hero numbers only |

- Tracking: `-0.01em` at 20–24px, `-0.02em` at 32px, none below 20px.
  Uppercase micro-labels (rare) get `+0.04em`.
- Wordmark: the sidebar brand renders `STARPORT` — uppercase, 600 weight,
  `+0.08em` tracking at 14px. It is the one uppercase display treatment;
  nothing else borrows it.
- `font-variant-numeric: tabular-nums` on every numeric column and every
  live-updating number. Costs, tokens, and latencies must not wiggle.
- Table headers: 12px/500, Text 3, sentence case. No all-caps letterspaced
  mono headers.

## Space, radius, motion

- **4px spacing base.** Components use 4/8/12/16/24/32/48.
- **Radius:** 4 (checkbox), 6 (buttons, inputs, chips), 8 (cards, dropdown
  panels), 12 (modals, composer card), 9999 (pills).
- **Motion:** 100–200ms `cubic-bezier(0.25,0.46,0.45,0.94)`; 300ms max for
  modal entry; no springs; honor `prefers-reduced-motion`. One orchestrated
  moment (page-body fade+4px rise on route change) beats scattered effects.
- **Skeletons** match final geometry (`--bg-raised` base, faint 1.8s
  shimmer). Spinners never occupy a page body; they are allowed inside
  buttons.

## Layout

### Shell

- Fixed left sidebar, **240px**, `--bg-panel`, 1px `--border-1` right edge.
  Collapsible to a 64px icon rail (state persisted).
- Nav item: 14px/500, 8px 12px padding, radius 6. Active = `--bg-hover`
  background + Text 1 + 2px accent bar on the left edge. Inactive = Text 3,
  hover Text 2.
- Sidebar footer: gateway status dot + version, theme toggle, GitHub link.
- Content region: max-width 1280px, 32px gutters, left-aligned within the
  remaining space.
- Three shell tiers, decided by viewport width. Test at 390, 768, 1024, and
  1280, plus 767 and 1023 as edge checks.
  - **Wide** (1024px and up): the 240px sidebar with the persisted collapse
    preference.
  - **Compact** (768–1023px): the 64px icon rail. "Expand" opens the full
    sidebar as an overlay that closes on navigation, Escape, or a click on
    the backdrop. The collapse preference does not apply.
  - **Phone** (below 768px): the sidebar becomes a left sheet behind a 48px
    top bar (menu trigger, wordmark, catalog chip, search). The content
    region drops to 16px gutters.
- The catalog chip and the page's primary action sit on the title line on
  the wide and compact tiers, and in the top bar on the phone tier.
- Page grids use container queries against the content column
  (`@2xl:`, `@3xl:`, `@4xl:`, `@5xl:`), never viewport prefixes, because the
  sidebar takes 64–240px of the viewport and a viewport breakpoint cannot
  know which. Two-column grids declare `grid-cols-1` below their threshold,
  so an implicit `auto` track never widens the page past the column.
- Dense tables declare priority columns: a column carries the table width
  it needs, and a narrower table drops it instead of clipping it behind a
  scrollbar.

### Page header

Identical on every page: title (20px/600) + one-line Text 3 description on
the left; the page's single primary action on the right. Optional tab row
beneath (active tab: Text 1 + 2px accent underline). No breadcrumbs.

### Density

Two documented modes, not per-page improvisation:

- **Dense** (Models, Usage, Keys, Presets tables): 40px rows, 10px 16px
  cells, 13–14px text, hairline row dividers, row hover `--bg-hover`.
- **Calm** (Overview, Settings, Chat, empty states): 16px body, 24px card
  padding, 48px between sections.

Cards are reserved for genuinely discrete objects (a provider, a stat, a
key). Sequential content uses flat sections with hairline dividers.

## Data display

- **IDs and keys** (the console says *ID*, never *slug*): mono 13px inside a subtle chip (`--bg-raised`,
  `--border-1`, radius 6) with a copy button on hover. Copy always confirms
  ("Copied") and never truncates silently — truncated values show head and
  tail: `STARPORT_02EH…C5KW`.
- **Secrets:** full value shown exactly once at creation, in a modal with a
  copy button; thereafter masked mono.
- **Prices:** OpenRouter grammar — `$0.22 / M in · $0.88 / M out`, mono,
  tabular, right-aligned in tables. Keep significant digits for sub-cent
  values; never scientific notation. Unknown price renders `—`, not `$0`.
  Per-unit prices scale to a readable denomination: tokens per 1M, document
  pages per 1K (`$1 / 1K pages`, never `0.001`).
- **Provider and author marks:** rendered through `EntityLogo`, which obeys
  the mark-treatment setting (`color` keeps each brand mark as shipped;
  `mono` flattens every mark to a single-tone glyph so the catalog reads as
  one set). Pages never bypass it with raw mark assets.
- **Refresh:** an icon-only button beside the staleness caption; the icon
  spins while the fetch runs. No "Refresh" label — the caption already
  names the data's age.
- **Token counts:** compact notation above 10k (`12.4k`, `1.2M`), exact below.
- **Latency:** ms below 1s, seconds with two decimals above (`740ms`,
  `2.81s`). Throughput as `54 tok/s`.
- **Timestamps:** relative (`4m ago`) with absolute UTC in a tooltip; Usage
  rows use absolute times when a range filter is active.
- **Status:** dot + label for liveness (`● healthy`), tint pill for lifecycle
  (`active`, `disabled`, `exhausted`). One vocabulary, defined here, used by
  every page.
- **Empty states:** icon + one sentence + one CTA + a mono snippet when the
  fix is a command (an empty Keys page shows the `curl` that creates one).
- **Charts** (Usage, Overview): Recharts via the shadcn chart wrapper.
  Neutral-step bars/lines; accent only for a selected series; area fills at
  low alpha; faint gridlines; tabular axis labels; endpoint emphasized.

## Components

Built on shadcn/ui (Base UI primitives), restyled through the tokens above.
Rules that override shadcn defaults:

- **Buttons:** primary = accent fill (one per viewport, law 1); secondary =
  `--bg-raised` + `--border-2`; ghost = text-only with hover bg; destructive
  = error solid, confirmation required for irreversible actions. Height 32px
  (dense contexts) / 36px (forms). Icon buttons are square with tooltips.
- **Inputs:** `--bg-raised`, `--border-2`, radius 6, 36px; focus = accent
  two-layer ring, no border color change alone.
- **Tables:** TanStack Table; header row 12px/500 Text 3 on transparent
  ground with a bottom hairline; sortable headers show direction on hover
  and when active; virtualized above ~100 rows. Dense catalog tables offer
  drag-resizable columns (thin handle on the header edge, double-click
  resets); the first column flexes to fill, the rest hold their size.
- **Selects:** the styled `Select` component only — `appearance-none` over
  `--bg-raised` with a Lucide chevron. The browser-default `<select>`
  chrome never ships. Multi-value filtering uses `FacetFilter`: a popover
  of checkbox facets with a search field above ~8 options, summarized in
  the trigger as `label · n`.
- **Popovers/dropdowns:** `--bg-raised`, `--border-2`, radius 8, shadow
  `0 8px 24px rgba(0,0,0,0.4)` + inset 1px white@0.05 ring (dark).
- **Sheets:** one `SheetContent` with a `side`. Detail panels enter from
  the right at 480px; navigation and the chat thread list enter from the
  left; pickers a thumb reaches enter from the bottom at up to 85vh.
- **Modals:** radius 12, same shadow contract, backdrop `black@0.6`;
  destructive modals restate the object name and require it or an explicit
  confirmation.
- **Toasts:** bottom-right, one line, auto-dismiss 4s, semantic left rule.
- **Command palette (⌘K):** global — navigation, model search, actions.
  Spotlight-scale: a centered ~640px panel with a large borderless input,
  not a small dropdown. The shortcut hint renders as `⌘ K` (spaced keys).
  `/` focuses inline search on catalog pages. Every list in the product is
  fully keyboard-navigable.
- **Credential resolution:** the provider detail page renders one numbered
  chain card in the keyring's true order (environment → gateway → BYOK by
  default), each source a row. Provider screens never link `/keys` and, in
  source, only `components/credentials/` files may say "BYOK" — both are
  test-enforced. Observed spend ("Paid by") stays a separate panel: it
  reports what happened, not what is configured.
- **Theme toggle:** shows the current state (the icon for the theme you are
  in), never the state it would switch to.

## Chat

The chat is a playground for operators: it exposes what consumer chat apps
hide (routing, cost, throughput) inside the modern composer pattern.

### Composer

One rounded card (radius 12, `--bg-raised`, `--border-2`), docked at the
bottom of a 768px-max thread column. On an empty thread it sits vertically
centered under a greeting with 3–4 dismissible starter prompts, then docks
after the first send.

```
┌──────────────────────────────────────────────────────────┐
│ [attachment chips — only when present]                   │
│ Auto-growing textarea (1–10 rows)                        │
│ ─────────────────────────────────────────────────────────│
│ [+]                    [★ model ▾] [effort ▾] [params] [⏎]│
└──────────────────────────────────────────────────────────┘
```

- **Bottom-left:** a `+` menu (attach image — enabled only when the selected
  model has vision; presets; future tools). Armed modes surface as removable
  chips.
- **Bottom-right:** the **model picker trigger** (`provider icon · short
  model name · ▾`), an effort selector when the model supports reasoning, a
  params popover (temperature, max tokens, routing), and the send button.
- Send: muted when empty → accent fill when text present → morphs to a Stop
  square during streaming (same position; Stop aborts upstream). Enter
  sends; Shift+Enter newline; Esc stops; drafts persist per thread.

### Model picker

Opens upward from the trigger, ~400px wide, max 60vh. ⌘K belongs to the
global palette everywhere, chat included — the palette searches models too.

1. Search field on top, autofocused, fuzzy over display name, provider, and
   exact model ID.
2. **Pinned** section first (star toggle on row hover, persisted), then
   presets (`@preset/…`), then providers as groups with icons — a list,
   never a grid.
3. Row: provider icon · display name · capability badge icons (vision,
   reasoning, tools) · right-aligned Text 3 `context · $in/$out per M`.
   The exact provider model ID appears as a mono second line. Unavailable
   models render dimmed with a status dot, not hidden.
4. Full keyboard navigation. Selection persists per thread and seeds new
   threads.

### Messages

- User: right-aligned bubble (`--bg-raised`, radius 12). Assistant: plain
  full-width markdown, no bubble.
- Streaming via Streamdown: incremental markdown, deferred code-block
  highlight until the closing fence, KaTeX and Mermaid supported.
- Reasoning models: a disclosure that auto-opens with "Thinking…" during the
  reasoning stream and auto-collapses to one line when the answer starts.
- **Metadata line (persistent, not hover-only)** under each assistant
  message, 12px mono Text 3:
  `model · routed provider · 412 tok · 54 tok/s · 1.24s · $0.0031`.
  Routed-provider visibility is Starport's differentiator — always show it.
- Hover actions: copy, retry (submenu: same model / choose model), edit on
  user messages, delete. Code blocks: language label + copy in a header bar;
  horizontal scroll inside the block only.
- Scroll: auto-follow at bottom; any upward scroll locks following and shows
  a "scroll to bottom" pill above the composer.

### Comparison

"Compare" attaches 2–4 models as chips beside the picker trigger. The
composer stays singular; one prompt fans out; responses render in responsive
columns, each with its own header (icon + name), stop/retry, and stats line.
"Continue with this model" collapses back to single-model mode.

## Voice

- Sentence case everywhere, including buttons and table headers.
- The product is "the Starport gateway" — the UI never says "LLM gateway".
  The CLI renders as code: `starport`. The wordmark stays uppercase
  `STARPORT` in the sidebar brand only.
- Documentation lives in the console at `/docs`, organized by persona
  (build against it · use an account · operate it), with copyable mono
  snippets that use the deployment's own origin.
- Controls name their outcome ("Create key", then a toast "Key created").
- Errors state what failed and the next action; no apologies, no codes
  without words.
- Numbers speak for the gateway: prefer "43 requests · 29 errors" over prose
  summaries.

## Accessibility

- AA contrast for all text except the documented Text 4 disabled step.
- Visible focus for every interactive element (accent two-layer ring).
- Full keyboard paths for every flow; roving focus in menus and pickers.
- `aria-live="polite"` with debounced announcements for streaming responses.
- 44px minimum touch targets on mobile; the model picker becomes a bottom
  sheet under 640px.

## Implementation mapping

- Tokens live in one CSS file as Tailwind v4 `@theme` variables; dark is the
  `:root` default, light under `.light` (explicit) with a
  `prefers-color-scheme` bootstrap. Components consume only role tokens
  (law 4).
- shadcn/ui components are generated then restyled at the token layer, not
  per-component.
- Fonts self-hosted as woff2 (latin, 400/500/600) via `@font-face`; no
  runtime font CDN.
- Charts use the shadcn chart wrapper over Recharts with the neutral series
  palette defined here.
