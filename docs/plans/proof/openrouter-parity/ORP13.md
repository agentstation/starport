# ORP13 — Closeout: docs, CI gate, green verifier

Branch: `codex/orp-13-closeout` (from `codex/orp-12-chat-compare`).
Pull request: #139. Commit: `e2fbcb0`.

## What shipped

- `scripts/verify-openrouter-parity.sh` now lives on the code train and runs
  in the CI release-contract job (`.github/workflows/ci.yml`), after
  `test-doc-link-verifier.sh`. This satisfies decision D7: every verify gate
  belongs to CI, the required-evidence list, or both. The AGENTS.md
  required-evidence list also names it.
- One probe correction: ORP-V16 grepped `compareMode`, a pre-implementation
  guess. The shipped symbol is `setCompareMode`. The probe now greps
  `setCompareMode` with the same assertion strength.
- `docs/ARCHITECTURE.md`: usage accounting, catalog freshness, presets,
  `provider.sort` / `max_price` completion, per-key budgets, and the embedded
  console moved out of the not-implemented list into shipped status. The
  package tree adds `internal/usage` and `internal/console`. The storage
  namespace table adds `internal/usage`: `usage:v1:`.
- `docs/TASKS.md`: one campaign row in Recently Completed with the per-PR
  breakdown (#126 plan; #127–#130 usage; #131–#132 catalog freshness; #133
  presets; #134–#135 routing preferences; #136–#137 budgets; #138 compare;
  #139 closeout).
- `README.md`: the version 1 scope now lists routing preferences, presets,
  budgets and allowed-model limits, usage accounting at `/api/v1/activity`,
  and the embedded console pages.
- The published design-review artifact
  (`claude.ai/code/artifact/9a62f796-c82f-4bb8-bd00-d6ef8ebea9d9`) roadmap
  section now reads "Roadmap — shipped" with per-item PR references, and the
  competitive-table request-log row flipped from "biggest gap" to shipped.

## PR train

Plan #126 (`codex/orp-0-plan`); implementation #127, #128, #129, #130, #131,
#132, #133, #134, #135, #136, #137, #138; closeout #139. All stack onto
console revamp #125, which bases on `main`.

## Fail-before

The verifier was authored red at ORP0 and recorded in
`proof/openrouter-parity/ORP0.md` (16 conditions, majority failing before
implementation). It stayed out of CI until this task per D7.

## Verification

- `bash scripts/verify-openrouter-parity.sh` → `Summary: 16 passed, 0 failed`,
  exit 0.
- `verify-starmap-ownership`, `verify-v1-architecture`,
  `test-dependency-direction-verifier`, `verify-dependency-direction`,
  `verify-catalog-driven-providers`, `verify-package-layout`,
  `verify-readme-quickstart`: all exit 0.
- `go test ./...` exit 0; `go vet ./...` exit 0; `make lint` exit 0;
  `make build` exit 0.
- `bash scripts/smoke-openrouter-sdks.sh` exit 0: 7 PASS (raw HTTP chat,
  stream, models, embeddings; Python, TypeScript, Go OpenRouter SDKs).
- Autoreview (Sol, thinking high, branch mode vs
  `origin/codex/orp-12-chat-compare`): clean, no accepted findings,
  overall 0.99.
