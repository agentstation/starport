# Starport agent instructions

Starport is an LLM inference gateway. It provides OpenAI-compatible routes at
`/v1` and OpenRouter-compatible routes at `/api/v1`.

## Work control

- Read `docs/TASKS.md` before a task. It is the status source of truth.
- Read `docs/ARCHITECTURE.md` before an architecture change.
- Follow the active durable plan when one exists. Keep its ledger and proof
  records current.
- Preserve unrelated worktree changes.
- Prefer direct breaking changes before the first release. Do not add legacy
  provider names, storage prefixes, or compatibility paths.

## Ownership boundaries

- Starmap owns provider IDs, model IDs, provider services, model offerings,
  capabilities, and prices.
- Starmap also owns catalog-acquisition authentication, status sources, and the
  immutable catalog generation.
- Starport owns inference credentials, tenant identity, routing policy,
  availability state, execution, caching, rate limits, and HTTP protocols.
- Derive provider and model facts from one Starmap snapshot. Do not add local
  provider switches, endpoint tables, model lists, or price defaults.
- Keep provider model IDs exact and opaque.
- Keep catalog-acquisition credentials separate from inference credentials.
- Keep Starmap acquisition, source, and sync option imports in
  `internal/catalog`. Application composition uses the catalog-owned refresh
  contract.

## Concept seams

- Put canonical inference types in `internal/inference`.
- Put catalog projection and generation rules in `internal/catalog`.
- Put deterministic route planning in `internal/routing`.
- Put attempt state and retry budgets in `internal/execution`.
- Put provider failure normalization in `internal/failure`.
- Put request credential placement in `internal/providers/auth`.
- Put cloud credential acquisition in `internal/credentials/cloudchain`.
- Put safe provider runtime projections in `internal/providers/state`.
- Put canonical response cache records in `internal/response/cache`.
- Make `internal/proxy` depend on `CacheManager` and
  `connectors.LeasingRegistry`, not concrete cache or registry adapters.
- Put protocol codecs in `internal/protocol/openai` and
  `internal/protocol/openrouter`.
- Keep composition in `internal/app` and HTTP wiring in `internal/server`.
- Access persisted identity, provider credentials, rate limits, presets, and
  response cache records through their concept-owned repositories.

## Required evidence

Run the checks that match the changed behavior. Before a pull request, run:

```bash
bash scripts/verify-starmap-ownership.sh
bash scripts/verify-v1-architecture.sh
bash scripts/test-dependency-direction-verifier.sh
bash scripts/verify-dependency-direction.sh
bash scripts/verify-catalog-driven-providers.sh
bash scripts/verify-package-layout.sh
bash scripts/verify-readme-quickstart.sh
bash scripts/verify-openrouter-parity.sh
bash scripts/benchmark-overhead.sh
go test ./...
go vet ./...
make lint
make build
bash scripts/smoke-openrouter-sdks.sh
```

`verify-catalog-driven-providers.sh` needs a Starmap source tree. It reads
`../starmap` by default. Set `CATALOG_DRIVEN_STARMAP_ROOT` to select another
tree, such as the published module that CI resolves.

Add contract tests at each changed seam. Do not weaken tests or verification
guards to hide a defect. Report skipped optional SDK checks as `UNVERIFIED`.
Every `scripts/verify-*.sh` gate belongs to CI, this list, or both. A gate that
no workflow runs cannot report a regression.

`scripts/verify-openrouter-parity.sh` guards the shipped OpenRouter parity
surface (conditions `ORP-V01` through `ORP-V16`) and runs in CI.

Use branches with the `codex/` prefix unless the task gives another name. Use
pull requests as the primary repository update method.
