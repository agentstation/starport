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
- Starport owns inference credentials, account identity, routing policy,
  availability state, execution, caching, rate limits, and HTTP protocols.
- Derive provider and model facts from one Starmap snapshot. Do not add local
  provider switches, endpoint tables, model lists, or price defaults.
- Keep provider model IDs exact and opaque.
- Keep catalog-acquisition credentials separate from inference credentials.
- Keep gateway API keys separate from provider credentials. A gateway API key
  authenticates a caller to Starport. A provider credential pays a provider.
- Use BYOK only for a provider credential an account brings for itself. A
  provider credential the operator shares with the deployment's accounts is a
  shared credential, and one read from the process environment is an
  environment credential.
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
- Put the provider credential sources, their scopes, and the selection
  strategies in `internal/providers/keyring`. It owns the words `environment`,
  `shared`, `byok`, and `anonymous`. No other package restates them.
- Put account identity, account-wide limits, and the default credential
  strategy in `internal/account`. Put the limit vocabulary itself in
  `internal/limits`, which both a gateway API key and an account use.
- Put the gateway authentication mode and its exposure rule in
  `internal/authmode`. Put the local admin token, launch tickets, and console
  sessions in `internal/localauth`.
- Put cloud credential acquisition in `internal/credentials/cloudchain`.
- Put safe provider runtime projections in `internal/providers/state`.
- Put canonical response cache records in `internal/response/cache`.
- Put stored file records, purposes, retention, and the stored-byte bound in
  `internal/files`. Put the bytes themselves in `internal/blob`, which owns the
  `filesystem` and `objectstore` backends and names the selected one. No other
  package names a backend, a bucket, or a storage path.
- Put document reading in `internal/document`. It owns the text layer, the page
  count, and the scanned verdict, and it reaches no network address. The engine
  vocabulary lives in `internal/inference`, and the page price comes from the
  catalog.
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
bash scripts/verify-v1-release.sh
bash scripts/verify-release-workflow.sh
bash scripts/verify-developer-experience.sh
bash scripts/verify-doc-links.sh
bash scripts/test-doc-link-verifier.sh
bash scripts/verify-openrouter-parity.sh
bash scripts/verify-console-modernization.sh
bash scripts/verify-auth-onboarding.sh
bash scripts/verify-console-session-grants.sh
bash scripts/verify-model-modalities.sh
bash scripts/verify-files-api.sh
bash scripts/verify-async-media-jobs.sh
bash scripts/verify-document-parser.sh
bash scripts/verify-reranking.sh
bash scripts/verify-catalog-performance.sh
bash scripts/verify-credential-sharing.sh
bash scripts/verify-action-pins.sh
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
This list holds every gate that blocks a pull request except the three that
need a release build: `verify-release-binaries.sh`, `verify-release-archives.sh`,
and `verify-homebrew-cask.sh` read a goreleaser `dist` tree, so the Release
Snapshot job owns them. Keep the list and the required CI jobs in step. A gate
that no workflow runs cannot report a regression.

`scripts/verify-openrouter-parity.sh` guards the shipped OpenRouter parity
surface (conditions `ORP-V01` through `ORP-V17`) and runs in CI. It grows only
when OpenRouter publishes a route outside the media surface.

`scripts/verify-auth-onboarding.sh` guards the separation of the credential
ideas: a gateway API key authenticates and owns nothing else, a provider
credential comes from the environment, the operator's shared plane, or an
account, only the account one is BYOK, authentication is required unless an operator disables it,
and the console reaches the gateway without holding a gateway key. It is
terminal at 26 conditions (`AON-V01` through `AON-V26`) and runs in CI.

`scripts/verify-console-session-grants.sh` guards how a console session is
minted: one registered set of grants, two machine-local ones that ship (a
launch ticket and the local admin token a reader pastes), an identity grant
that ships inert and is filled only when an operator configures an identity
provider (`internal/identity` performs the OAuth dance and the composition
root fills the slot), one first-contact page outside the shell that states
its trust scope, and the words *sign in* reserved for the identity grant's
own surfaces alone. It is terminal at 16 conditions (`CSG-V01` through
`CSG-V16`) and runs in CI.

`scripts/verify-model-modalities.sh` guards the media surface: the modality
vocabulary, the operation set, the catalog projection that names what each
offering serves, the eight dedicated media routes, the two media scopes, and
the console facet that reads output modalities. It is terminal at 26 conditions
(`MMD-V01` through `MMD-V26`) and runs in CI.

`scripts/verify-files-api.sh` guards the file store: the five routes and their
two scopes, the record and byte split across `internal/files` and
`internal/blob`, both backends, the retention window, the stored-byte bound,
the stored file reference a chat request carries, and the console file view. It
is terminal at 22 conditions (`FIL-V01` through `FIL-V22`) and runs in CI.

`scripts/verify-async-media-jobs.sh` guards the asynchronous job surface. It
covers the job record, its five states, the five video routes, and their one
scope. It covers the provider job identifier that never reaches a caller. It
covers the retention window, the outstanding job bound, the poll budget that
ends an abandoned job, and the console jobs page. It is terminal at 18
conditions (`AMJ-V01` through `AMJ-V18`) and runs in CI.

`scripts/verify-document-parser.sh` guards the document parser plugin. It covers
the typed `file-parser` option and the two engines this gateway runs. It covers
the refusals an unknown engine and an unenforced plugin draw. It covers the
in-process read that reaches no provider. It covers the recognition route, its
page charge, the extraction cache, the spend bound, and the console view of
what a page cost. It is terminal at 20 conditions (`PLG-V01` through `PLG-V20`) and
runs in CI.

`scripts/verify-reranking.sh` guards reranking. It covers the canonical types,
the transport descriptor, and the connector call. It covers the two codecs and
every wire name each one owns. It covers the two routes and the `rerank:write`
scope that stands alone. It covers operation-aware planning and the document
bound.

It covers the search unit in the usage record and the spend refusal before the
provider call. It covers the console surface that names what a model serves. It
is terminal at 22 conditions (`RNK-V01` through `RNK-V22`) and runs in CI.

That gate owns the media surface alone. The parity gate
`scripts/verify-openrouter-parity.sh` keeps its own count of 17 and its own
stated meaning, so a new media route does not move it. Re-open the split when OpenRouter changes a route that
the parity gate already guards.

`scripts/verify-credential-sharing.sh` guards the credential-sharing and
identity campaign. It covers the relational contract in `internal/sqlstore`
(embedded SQLite with a PostgreSQL or MySQL connect), the many shared
credentials per provider with per-credential grants in
`internal/providers/keyring`, the operator BYOK policy and account provider
and model access in `internal/account`, account templates that stamp
creation defaults, the people plane in `internal/identity` (users, teams,
and account grants acquired through gothic OAuth or WorkOS SSO), and the
console surfaces that render them. It is terminal at 23 conditions
(`CSH-V01` through `CSH-V23`) and runs in CI.

Use branches with the `codex/` prefix unless the task gives another name. Use
pull requests as the primary repository update method.
