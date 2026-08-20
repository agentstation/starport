# ORP9 proof — console presets page and chat routing preferences

Branch: `codex/orp-9-console-routing` (commit 077e48c). PR: #135, stacked on
#134 (`codex/orp-8-provider-prefs`).

## Fail-before evidence

Captured on the base commit before the change:

- `internal/console/handler.go` `PagePaths` had no `/presets` entry, and
  `GET /presets` returned 404 from the console handler.
- `GET /static/js/pages/presets.js` returned 404 (file absent).
- The six new `TestChatRoutingControlsShip` assertions failed (routing
  popover markup, provider order/only/ignore inputs, sort select, preset
  picker section, unenforced metadata span, and api.js header capture all
  absent).

## Change summary

- New `/presets` page (`internal/console/static/js/pages/presets.js`) with
  typed create, edit, and delete forms over the presets API, plus nav entry,
  route registration, and `TestPresetsPageIsRouted`.
- Chat page (`chat.js`): routing popover with provider `order`, `only`,
  `ignore`, and `sort` controls; preset picker that sends `@preset/<name>`
  as the request model; unenforced-provider-fields marker in the response
  metadata line fed by the `X-Starport-Unenforced-Provider-Fields` response
  header captured in `api.js`.
- `internal/presets`: `sort`, `max_prompt_price_per_1m`, and
  `max_completion_price_per_1m` fields with validation
  (`model_test.go` added); merge threading in `internal/proxy/presets.go`.
- `internal/server/controllers/base.go`: `logError` now logs provider
  failure details and the wrapped cause, so a 503 names its root cause
  ("no models available for routing") instead of only the safe message.
- Defect fixed during the walkthrough: the chat popover state declarations
  (`modelPop`, `paramsPop`, `routingPop`) sat after `render()`'s cleanup
  `return`, so the bindings stayed in the temporal dead zone and every
  popover toggle threw `ReferenceError`. Declarations moved before the
  return; duplicate dead declarations removed.

## Acceptance walkthrough (browser, localhost:8080)

1. Created `@preset/fast-groq` ("Fast Groq routing with price sort",
   order `groq`, sort `price`) with the typed form; edited it to model
   `groq/compound-mini`; the table row shows the routing badges
   `sort price` and `order groq`.
2. In chat, selected `@preset/fast-groq` from the model picker's preset
   section and sent a message. The gateway resolved the preset and the
   response metadata names the served route `groq/compound-mini`
   (screenshot `chat-preset-response.jpg` in the session proof set;
   header badge shows `@preset/fast-groq`, assistant meta shows
   `groq/compound-mini`, ttft 1.49s).
3. Unenforced fields (D3): the console popover exposes only enforced
   fields by design, so the D3 path was proven over the wire:

   ```
   POST /api/v1/chat/completions
   {"model":"@preset/fast-groq", "provider":{"zdr":true,"data_collection":"deny"}, ...}
   → HTTP/1.1 200 OK
   → X-Starport-Unenforced-Provider-Fields: data_collection,zdr
   ```

   `chat.js` renders that header value as the `unenforced …` span in the
   message metadata line (`TestChatRoutingControlsShip` asserts the span
   and the api.js capture).

Environment note: the walkthrough ran against a re-initialized local store
(admin key re-minted). The catalog snapshot
(`catalog-20260819T233823Z-4b9bfb67cb93`) lists two groq models, so the
preset was pointed at `groq/compound-mini`; earlier 503s for
`groq/llama-3.3-70b-versatile` were correct not-in-catalog refusals, made
diagnosable by the new failure-cause logging.

## Verification

- 7/7 verify scripts pass: starmap-ownership 12/12, v1-architecture 12/12,
  dependency-direction verifier + SP-D 6/6, catalog-driven-providers 19/19,
  package-layout, readme-quickstart.
- `go test ./...`: 37 packages ok, exit 0.
- `go test ./internal/console/ -count=1`: 13/13 pass, including
  `TestPresetsPageIsRouted` and `TestChatRoutingControlsShip`.
- `go vet` clean; `make lint` 0 issues; `make build` ok.
- `smoke-openrouter-sdks.sh`: Python, TypeScript, and Go SDK PASS.
- Autoreview branch mode vs `origin/codex/orp-8-provider-prefs`
  (profile auto → sol gpt-5.6-sol, thinking high): clean, 0 findings,
  "patch is correct (0.98)".
