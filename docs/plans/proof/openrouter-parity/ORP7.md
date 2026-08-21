# ORP7 — Presets: typed config, CRUD API, @preset/ resolution

Status: done. PR: [#133](https://github.com/agentstation/starport/pull/133)
(`codex/orp-7-presets` → `codex/orp-6-console-freshness`).
Commit `5256278`.

## Fail before

- Fail-first probe against the pre-change gateway (temporary
  `preset_failbefore_test.go`, deleted after capture): a chat request
  with `"model": "@preset/mock-default"` returned
  `503 {"error":{"code":503,"message":"The provider request
  failed.","metadata":{"error_type":"service_unavailable"}}}` — the
  reference leaked into routing as a literal model ID and produced a
  misleading provider failure, worse than the model-not-found the spec
  predicted.
- Acceptance tests written first and run red: `TestPresetCRUDEndpoints`
  404 on every `/api/v1/presets` route (endpoints absent);
  `TestChatResolvesPresetReference` hit the 503 above.
- The old `presets.Preset` carried `ID`/`Version` fields and an untyped
  `map[string]any` config with no validation sentinel.

## What landed

- **Typed config** (`internal/presets/model.go`): `Config` with model,
  fallback `Models`, `*ProviderPreferences` (order/only/ignore/
  allow_fallbacks), system prompt, and sampling overrides
  (temperature, top_p, presence/frequency penalty, max_tokens, seed,
  stop). `Preset` drops `ID`/`Version` (pre-release breaking change).
  `Validate()` and repository name checks wrap the new
  `presets.ErrInvalidPreset` sentinel; `Config.Clone()` deep-copies
  records out of the repository.
- **REST surface** (`internal/server/controllers/presets.go`, routes in
  `routes.go`): `GET /api/v1/presets` and `/{name}` for any
  authenticated key; POST/PUT/DELETE behind
  `requireAnyScope("presets:write")` (admin `*` wildcard passes). PUT
  requires the expected `revision` (0 → 400); DELETE takes an optional
  `?revision=` guard. Strict JSON decode rejects unknown fields. Name
  is immutable. Nil repository degrades every handler to a loud 503.
- **Chat resolution** (`internal/proxy/presets.go`):
  `proxy.PresetResolver` middleware (Wrap idiom, mirrors
  `UsageCapture`) resolves `"@preset/<name>"` model references and the
  new `preset` body field on both chat paths **before** caching and
  routing, so cache keys and routes see the resolved request. Merge
  semantics per D5: request fields win per-field for sampling; the
  preset owns model selection when the reference form is used; the
  system prompt is prepended only when the request has no system
  message; provider preferences apply whole-object.
- **Error contract**: `proxy.ErrPresetNotFound` → `404
  not_found_error` naming the unresolved reference
  (`controllers/base.go`); a preset-storage failure stays a 500 and is
  never conflated with not-found.
- **Composition** (`internal/app/app.go`): `proxy.New →
  PresetResolver.Wrap → UsageCapture.Wrap`; the server test harness
  (`test_helpers_test.go`) mirrors the same order. `preset` wire field
  threaded through the openrouter codec → `DecodedChat` → controller.
- Storage README key-pattern table corrected to
  `presets:v1:name:{base64url(name)}`.

## Acceptance evidence

1. `TestPresetCRUDEndpoints`: red (404s) then green — scope split
   (reader 403 on writes, writer 201/200/204), duplicate 409, stale
   revision 409, revision-0 update 400, delete-then-get 404.
2. `TestChatResolvesPresetReference`: red (503) then green —
   `@preset/mock-default` returns 200 on **both** `/api/v1` and `/v1`
   chat completions; the `preset` body field resolves; `@preset/missing`
   returns 404.
3. `TestChatReferenceMergesPresetConfig`,
   `TestPresetRequestFieldsWin`, `TestUnknownPresetRejected`
   (`internal/proxy`): merge semantics, request-wins precedence,
   system-prompt prepend, provider whole-object replacement,
   stream-path rejection, storage-failure ≠ not-found, and untouched
   pass-through without a preset.
4. Full suite: `go test ./...` exit 0, 37 packages ok.

## Gates

All on `codex/orp-7-presets` at `5256278`: the seven
`scripts/verify-*.sh` gates exit 0 (catalog-driven 19/19 PASS);
`go test ./...` exit 0; `go vet` exit 0; `make lint` exit 0 (one
goconst finding — three `"not_found_error"` literals — fixed by
hoisting `errorTypeNotFound` into the existing const block);
`make build` exit 0; `smoke-openrouter-sdks.sh` PASS for Python,
TypeScript, and Go. Autoreview (`--mode branch --base
origin/codex/orp-6-console-freshness`, Sol high): clean, 0.98, no
findings.

## Deviations and follow-ups

- `Preset.ID` and `Preset.Version` were removed rather than preserved:
  the name is the natural key and the repository `Record.Revision`
  already owns optimistic concurrency. Pre-release breaking changes
  are allowed by repository policy.
- `FallbackModels` from a preset fill in only when the request supplied
  none, so a request that pins its own model never inherits surprise
  fallbacks.
- Console preset management UI is ORP9, not this task.
