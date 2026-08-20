# ORP12 — Chat comparison mode

Branch `codex/orp-12-chat-compare` from `codex/orp-11-console-budgets`.
Commit `c0ed352` (3 files, +286/−6). PR #138.

## What shipped

- A `compare` toggle in the chat topbar. Active compare mode swaps the
  thread for a compare host, seeds the model list with the current
  model, and turns the picker into `add model (n/4)`.
- Two to four models answer one prompt in parallel streamed columns.
  Duplicate adds and the four-model cap toast instead of failing
  silently. Chips with remove buttons list the selection.
- Each column reports the serving provider (`via groq`), time to first
  token, total latency, token count when usage arrives, and cost from
  the catalog snapshot's per-token pricing. When the snapshot has no
  rates the foot says `no pricing` — the explicit reason required by
  plan invariant 3.
- A failed provider shows its error inside its own column and the foot
  says `failed`; the other columns stream on. An aborted run says
  `stopped`. A column that streams no content renders
  `The model returned no content.` instead of an empty box.
- Single-model chat is unchanged. `new chat` and selecting a saved
  conversation leave compare mode. Comparisons are ephemeral.

## Fail-before

- Guard test red without the change: `git stash push` of chat.js +
  chat.css → `TestChatComparisonShips` FAIL (missing compareSend,
  compare-grid, compareCost, "no pricing", "at most four models",
  `.compare-col`) → stash pop → PASS.
- The pre-change chat page has no compare control (topbar held only the
  model picker and price hint).

## Walkthrough (browser, tab 167376998, gateway from this build)

1. Set the single model to Compound Mini, click `compare` — the chip
   seeds with the current model, the welcome panel explains the mode,
   the picker reads `add model (1/4)`.
2. Add `groq/compound` and `anthropic/claude-haiku-4-5-20251001` via
   the picker (the picker stays open for multi-add; the haiku row shows
   catalog pricing `$1 in / $5 out per 1M`).
3. Send "In two sentences, what is an LLM gateway?" — three columns
   render side by side and stream in parallel. compound-mini answered
   (`via groq ttft 0.63s 1.4s total no pricing`); the anthropic column
   failed inside its own column (`The provider request failed.`, foot
   `failed`) without stopping the others.
4. Second prompt in the same session appended a second grid:
   compound produced a full markdown answer with a table
   (`via groq ttft 0.78s 2.8s total no pricing`), compound-mini
   returned no content and said so, anthropic failed per-column again.
5. Toggled compare off, sent "Say hello in exactly four words." —
   single-model chat regression-checked: normal thread, reasoning
   expander, meta line `ttft 0.28s`, conversation saved to the sidebar.

## Defect found and fixed during the walkthrough

The first render collapsed three columns to the two-column fallback:
this Chrome profile drops `style` attributes entirely (CSSOM stays
empty even for `color: red`, with CSP `style-src 'unsafe-inline'`
present), so the `--compare-cols` custom property never applied. Fixed
by setting the property through the CSSOM
(`grid.style.setProperty("--compare-cols", …)`), which is immune.

## Environmental constraints (recorded, not defects in this change)

- The catalog snapshot holds exactly two routable models
  (`groq/compound`, `groq/compound-mini`); every other probed offering
  returns 503 `no models available for routing` or 401 missing
  credentials. The third column therefore demonstrates the per-column
  failure path rather than a third stream.
- Outbound calls to api.anthropic.com fail locally with a TLS
  certificate-verification error (x509 interception); groq works from
  the same process. So no priced model can complete here, and the cost
  foot demonstrates the `no pricing` reason branch live; the numeric
  branch is `compareCost` (usage × catalog per-token rates via
  `formatNanoUSD`).
- Groq streams do not carry usage (compound-mini streamed content with
  no usage object; compound streamed usage all-zeros), so token counts
  cannot populate live. Recorded in the open-defects list: streaming
  usage normalization, and `groq/compound` streaming only a single
  `reasoning` delta with empty content for some prompts.

## Verification

- `go test ./internal/console/ -count=1` green with the change, red
  without it (guard test above); `go test ./...` clean.
- Seven verify gates exit 0: starmap-ownership, v1-architecture,
  dependency-direction-verifier, dependency-direction,
  catalog-driven-providers, package-layout, readme-quickstart.
- `go vet` clean, `make lint` 0 issues, `make build` complete,
  `smoke-openrouter-sdks.sh` PASS ×3 (Python, TypeScript, Go).
- Autoreview branch mode vs `origin/codex/orp-11-console-budgets`:
  codex gpt-5.6-sol high, clean, overall 0.98, zero accepted findings.
