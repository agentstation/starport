# Starport v1 Architecture Plan

Status: `active`

Owner: plan

Created: 2026-08-03

Baseline: master @ bf6b09e11369 with 75 pre-existing dirty worktree entries

Proof root: `docs/proof/starport-v1-architecture/`

Next action: commit the verified Starport campaign, run the pre-PR review, merge the protected final pull request, and execute SVA11 cleanup.

## Outcome

> Starport v1 processes each request through one canonical inference model, one immutable Starmap catalog generation, one route plan, one attempt executor, and concept-owned persistence seams. OpenAI and OpenRouter adapters expose this behavior without owning business policy. Production startup fails closed, retries obey one budget, caches preserve tenant and request semantics, and automated contracts prove each boundary.

## Architecture

Before:

```text
[HTTP server: transport + composition]
    -> [proxy: DTOs + cache + use cases + stream fallback]
    -> [router: planning + execution + health + retry]
    -> [registry: config + environment + factory + lifecycle + discovery]
    -> [connectors]

[catalog globals] <-> [registry] <-> [router]
[raw KV] <-> [controllers, cache, BYOK, rate limit]
```

After:

```text
[OpenAI/OpenRouter adapters]
    -> [gateway use cases]
    -> [canonical inference seam]
    -> [pure route planner] -> [immutable route plan]
    -> [attempt executor] -> [provider adapter]

[Starmap acquisition auth] -> [provider catalog APIs] -> [Starmap catalog snapshot]
[Starmap catalog snapshot] + [runtime availability snapshot]
    -> [routable snapshot] -> [route planner]

[Starport inference auth] -> [provider adapter] -> [provider inference API]

[identity | credentials | rate limit | preset | response cache repositories]
    -> [KV adapter: Badger or Valkey]

[app bootstrap] constructs and owns every runtime dependency.
```

## Scope

- Owns: canonical inference, failures, Starmap, route planning, attempt execution, and runtime availability.
- Owns: cross-repository Starmap provider, catalog-acquisition auth, endpoint, reliability-source, definition, and offering facts required by Starport v1.
- Owns: concept repositories, response-cache safety, composition, protocol adapters, package cleanup, and architecture verification.
- Does not own: content moderation, billing, analytics, webhooks, SSO, RBAC, or the admin dashboard. `docs/PLAN.md` owns those product features.
- Does not own: OpenRouter account, credit, workspace, OAuth, media, rerank, or hosted platform parity. The Starport v1 product roadmap owns later API breadth.
- Non-goals: a big-bang rewrite, a new storage engine, external deployment, publication, or a plugin SDK without an active consumer.

## Invariants

1. One package owns each business invariant, state transition, error class, and durable key schema.
2. One request uses one catalog generation and one immutable route plan.
3. Starmap owns catalog facts. Starport owns runtime state and routing policy.
4. The route planner cannot access network, storage, environment, or clocks.
5. The attempt executor owns the total retry and fallback budget.
6. Provider adapters make one logical attempt and normalize provider-specific results.
7. Starport never hides a provider change after it sends the first response byte.
8. Production construction never selects a mock dependency implicitly.
9. Protocol DTOs do not become canonical business objects.
10. Concept repositories version durable records and test each schema contract.
11. Cache identity includes tenant, request semantics, routing policy, and catalog generation.
12. Logs, errors, events, fixtures, and proof files never contain credentials or prompt data by default.

## Findings ledger

| ID | Classification | Evidence | Owning task |
|---|---|---|---|
| SVF-001 | blocker, high confidence | Inference types exist in connectors, proxy, cache, DTO, and `pkg/providers`. | SVA2, SVA9 |
| SVF-002 | blocker, high confidence | Embedded catalog globals, connector discovery, registry, and router share model truth. | SVA3 |
| SVF-003 | blocker, high confidence | Router and proxy own different streaming and non-streaming fallback paths. | SVA4, SVA5 |
| SVF-004 | blocker, high confidence | Connector retry, router fallback, two circuit mechanisms, and registry health lack one budget. | SVA5 |
| SVF-005 | blocker, high confidence | Server constructs router, proxy, provider keys, and mock storage. Registry reads environment values and selects a mock provider. | SVA8 |
| SVF-006 | blocker, high confidence | Provider-key writes use `provider_key:` while global listing scans `providerkey:`. | SVA1, SVA6 |
| SVF-007 | blocker, high confidence | Response-cache identity omits tenant, fallback, provider policy, reasoning, user, and catalog generation. | SVA7 |
| SVF-008 | reliability, high confidence | `pkg/providers` differs from the connector contract and has no production consumer. | SVA9 |
| SVF-009 | reliability, high confidence | Critical orchestration coverage ranges from 37.8% to 59.1%. | SVA0-SVA10 |
| SVF-010 | resolved blocker, high confidence | Starmap now has Mistral, Azure OpenAI, and Ollama provider records. Active routes require the provider, offering, adapter, and configuration intersection. | SVA12-SVA14 |
| SVF-011 | resolved security issue, high confidence | Google inference credentials use the registered header and never enter a URL. | SVA14 |
| SVF-012 | resolved blocker, high confidence | One adapter registry owns inference credential membership and validation. Unsupported Bedrock inference credentials fail closed. | SVA14 |
| SVF-013 | resolved blocker, high confidence | Embeddings require an exact Starmap offering operation, endpoint, and compiled adapter capability. | SVA15 |
| SVF-014 | resolved blocker, high confidence | Discovery projects one immutable routable snapshot and makes no provider discovery call. | SVA15 |
| SVF-015 | resolved blocker, high confidence | Exact provider model IDs remain opaque. Starmap selects Vertex author protocols, non-stream paths, and stream paths. | SVA13, SVA15 |
| SVF-016 | resolved reliability issue, high confidence | Static probe models, Gemini-only filtering, and fake Azure deployments are deleted. | SVA14, SVA15 |
| SVF-017 | resolved financial issue, high confidence | Dormant billing and sample-price fallbacks are deleted. Exact offering prices are the only price facts. | SVA15 |
| SVF-018 | resolved reliability issue, high confidence | Starmap supplies service URLs, prompt-cache facts, provider metadata, and endpoints. One Starport adapter registry owns inference authentication. | SVA13-SVA16 |
| SVF-019 | resolved verification issue, high confidence | The ownership verifier and mutation contracts fail on duplicated provider facts and prove catalog propagation. | SVA12, SVA16 |
| SVF-020 | resolved architecture issue, high confidence | Starmap acquisition is the only dynamic catalog update path. Inference connectors do not discover models. | SVA13-SVA15 |
| SVF-021 | resolved release issue, high confidence | Starmap v0.3.0 is public and immutable. Starport pins the released module without a local replacement. Both terminal verifiers report 12/12. | SVA16 |

## Decisions

| ID | Date | Decision | Evidence and consequence | Re-open condition |
|---|---|---|---|---|
| SVD-001 | 2026-08-03 | Apply DRY to semantics and rules, not to wire shapes. | Protocol and provider DTOs can repeat fields, but they convert once through the canonical inference seam. | A measured allocation or latency limit requires generated adapters. |
| SVD-002 | 2026-08-03 | Use Starmap as the only catalog authority. | Catalog definitions and offerings come from one immutable generation. Starport runtime state remains separate. | Starmap cannot express a required factual attribute after a reviewed schema proposal. |
| SVD-003 | 2026-08-03 | Split pure route planning from attempt execution. | Deterministic policy tests no longer need provider mocks. Streaming and non-streaming share one plan. | A measured hot-path budget proves the boundary causes unacceptable cost. |
| SVD-004 | 2026-08-03 | Give the executor one total retry budget. | Provider, transport, and fallback attempts cannot multiply without a bound. | A provider protocol requires a hidden transport retry that cannot expose attempt evidence. |
| SVD-005 | 2026-08-03 | Ship v1 as a binary-first product. | Unused public packages carry no compatibility promise. A plugin SDK needs a separate plan and consumer. | A named external consumer and versioning owner request the SDK. |
| SVD-006 | 2026-08-03 | Use versioned concept namespaces and make pre-launch internal schema changes directly. | Starport has no released durable-data contract. Compatibility branches add failure paths without protecting a consumer. OpenRouter wire compatibility remains product behavior. | A released Starport version has persisted operator data or a named external consumer. |
| SVD-007 | 2026-08-03 | Treat Starmap as the database and schema owner for provider-hosted model facts. | Starmap owns provider identity, catalog-acquisition auth metadata, provider endpoints and status sources, definitions, offerings, price, limits, lifecycle, and evidence. Starport owns gateway and inference authentication, encrypted inference credentials, tenant policy, and runtime state. | A fact cannot be represented after a reviewed Starmap schema proposal. |
| SVD-008 | 2026-08-03 | Define an active provider as the intersection of a Starmap provider, a compiled Starport adapter, and valid operator configuration. | Catalog data cannot manufacture adapter code, and adapter code cannot manufacture catalog facts. Startup fails closed on a configured provider outside the intersection. | Starport supports runtime-loaded adapters with a signed contract. |
| SVD-009 | 2026-08-03 | Materialize tenant and local serving facts into a Starmap generation without embedding them as global facts. | Azure deployment names and local Ollama inventory vary by installation. They remain operator or runtime observations, but their normalized definitions and offerings enter the same immutable Starmap generation used by discovery and routing. | Starmap gains a separate first-class tenant catalog product with a stronger ownership model. |
| SVD-010 | 2026-08-03 | Use Starmap acquisition as the only dynamic provider-model update path. | Starmap already loads provider credentials, constructs protocol-specific provider clients, fetches model catalogs, reconciles evidence, and publishes atomic generations. Starport adapters perform inference only. | An inference provider exposes no catalog surface and Starmap records an explicit observation adapter requirement. |
| SVD-011 | 2026-08-03 | Keep catalog-acquisition and inference authentication as isolated concepts. | Starmap uses API keys, cloud chains, and workload identity only to build the map. Starport uses its own encrypted credentials and BYOK only for inference. Neither resolver can read or reuse the other plane's secret values. | A provider documents one token as intentionally shared and an explicit operator policy enables the bridge. |

## Status ledger

| ID | Task | Status | Evidence |
|---|---|---|---|
| SVA0 | Pin the baseline, create the proof root, add the red verifier, and capture architecture fitness evidence without production changes. | `done` | 2026-08-03. Verifier: 1 passed, 11 failed. Full Go suite passed. Plan lint: zero diagnostics. Proof: `docs/proof/starport-v1-architecture/sva0-baseline.md`. |
| SVA1 | Unify provider-key storage identity and prove one canonical schema. | `done` | 2026-08-03. The owner selected a direct pre-launch break. The canonical lifecycle contract and BYOK race suite pass. V06 and V12 pass. Proof: `docs/proof/starport-v1-architecture/sva1-provider-credential-storage.md`. |
| SVA2 | Create the canonical inference and failure seams, then adapt current callers. | `done` | 2026-08-03. Chat, embeddings, streams, provider failures, and HTTP failures cross canonical seams. Package, full-suite, race, and 10-second fuzz gates passed. Proof: `docs/proof/starport-v1-architecture/sva2-canonical-inference.md`. |
| SVA3 | Integrate Starmap and create generation-consistent catalog and routable snapshots. | `done` | 2026-08-03. Starmap v0.2.0, atomic catalog generations, separate adapter availability, and routable discovery/planning snapshots are active. Package, full-suite, and race gates passed. Proof: `docs/proof/starport-v1-architecture/sva3-starmap-catalog.md`. |
| SVA4 | Extract a pure deterministic route planner that returns immutable plans. | `done` | 2026-08-03. The transport-free planner owns deterministic policy, rejection evidence, and immutable attempts. Unit, fuzz, benchmark, race, import-graph, and full-suite gates passed. Proof: `docs/proof/starport-v1-architecture/sva4-route-planner.md`. |
| SVA5 | Create the attempt executor, state machine, retry budget, and single runtime availability owner. | `done` | 2026-08-03. One executor now owns total attempt and elapsed-time budgets for streaming and non-streaming plans. One offering tracker owns availability. Provider, transport, proxy, and Vertex adapter retry loops are gone. Package, full-suite, race, vet, and verifier gates passed. Proof: `docs/proof/starport-v1-architecture/sva5-attempt-execution.md`. |
| SVA6 | Put API keys, provider credentials, rate limits, and presets behind versioned concept repositories. | `done` | 2026-08-03. Four direct version 1 repositories own schemas and CAS rules. Controllers, middleware, ChatUI, and BYOK use repository contracts. Memory, Badger, race, vet, architecture, and full-suite gates passed. Valkey is `UNVERIFIED` because `TEST_VALKEY_URL` is not set. Proof: `docs/proof/starport-v1-architecture/sva6-concept-repositories.md`. |
| SVA7 | Replace response-cache identity and records with tenant-safe canonical semantics. | `done` | 2026-08-03. One response-cache seam owns tenant-safe semantic keys, versioned canonical results, eligibility, and stream replay. Pairwise, corruption, partial-stream, race, fuzz, vet, architecture, and full-suite gates passed. Proof: `docs/proof/starport-v1-architecture/sva7-tenant-safe-response-cache.md`. |
| SVA8 | Move all runtime construction, configuration conversion, start, and close ownership into app bootstrap. | `done` | 2026-08-03. App bootstrap now owns typed configuration conversion, production construction, explicit start, rollback, and reverse close. Server and registry receive ready dependencies and cannot select mocks. Proof: `docs/proof/starport-v1-architecture/sva8-composition-lifecycle.md`. |
| SVA9 | Separate OpenAI and OpenRouter protocol adapters, resolve the public package boundary, and delete duplicate ownership. | `done` | 2026-08-03. Separate protocol codecs, route-scoped errors, canonical proxy values, a binary-first package boundary, Starmap-only catalog facts, exact provider IDs, and raw/optional SDK smoke checks are active. Proof: `docs/proof/starport-v1-architecture/sva9-protocol-public-boundary.md`. |
| SVA10 | Run final protocol, architecture, race, fault, security, benchmark, and documentation gates. | `done` | 2026-08-03. Verifier 12/12, full suite, race, fuzz, fault, security, vulnerability, benchmark, lint, build, container, Valkey, smoke, writing, and diff gates passed. Optional official SDK packages remain `UNVERIFIED`. Proof: `docs/proof/starport-v1-architecture/sva10-final-gates.md`. |
| SVA12 | Audit hardcoded catalog facts and add a red ownership verifier. | `done` | 2026-08-03. Ownership verifier: 0 passed, 12 failed. Architecture verifier: 12 passed, 0 failed. Shell syntax and lint pass. Writing lint has zero diagnostics. Proof: `docs/proof/starport-v1-architecture/sva12-hardcoded-catalog-facts.md`. |
| SVA13 | Extend Starmap provider contracts and authoritative provider data. | `done` | 2026-08-03. Typed acquisition auth, workload identity, provider inference services, offering operations, and Mistral/Azure/Ollama records now live in Starmap. Catalog, race, vet, lint, generation, docs, and validation gates pass. Proof: `docs/proof/starport-v1-architecture/sva13-starmap-provider-contracts.md`. |
| SVA14 | Replace Starport provider and credential fact ownership with catalog projections. | `done` | 2026-08-03. One adapter registry now owns inference adapter contracts. Active providers require a Starmap offering, a compiled adapter, and valid operator configuration. Acquisition and inference credentials remain isolated. The full suite and focused race gates pass. Proof: `docs/proof/starport-v1-architecture/sva14-catalog-backed-provider-activation.md`. |
| SVA15 | Route discovery, capabilities, endpoints, exact IDs, cache support, and prices through one snapshot. | `done` | 2026-08-03. Discovery and routing now project exact operations, endpoints, protocols, IDs, cache support, and prices from one snapshot. Both full suites, focused race gates, vet, diff, and ownership O01-O12 pass. Proof: `docs/proof/starport-v1-architecture/sva15-snapshot-only-provider-facts.md`. |
| SVA16 | Run cross-repository ownership, security, protocol, race, and final review gates. | `done` | 2026-08-04. Starmap PRs #64 and #65 merged. Public immutable v0.3.0 and its Homebrew install pass. Starport pins v0.3.0 without a replacement. Both terminal verifiers report 12/12, and all final gates pass. Proof: `docs/proof/starport-v1-architecture/sva16-starmap-ownership-final.md`. |
| SVA11 | Clean up after the final pull request merges by deleting this plan, its proof root, and its index entry. | `todo` | Runs after SVA16 is terminal and the final Starport pull request merges. |

## Verifier contract

`bash scripts/verify-v1-architecture.sh` owns 12 fixed conditions. It prints one result for each condition and a final `Summary: N passed, M failed` line.

The SVA0 baseline must be red. The completion gate requires `Summary: 12 passed, 0 failed`.

| Condition | Required terminal evidence |
|---|---|
| V01 | Starport uses the Starmap-compatible Go floor and a tagged Starmap module. |
| V02 | The canonical inference contract test exists and passes. |
| V03 | The generation-consistent routable snapshot test exists and passes. |
| V04 | The deterministic route planner contract test exists and passes. |
| V05 | The attempt state and retry-budget contract test exists and passes. |
| V06 | The versioned identity, credential, rate-limit, and preset repository contracts exist and pass. |
| V07 | Response-cache semantic key and tenant isolation tests exist and pass. |
| V08 | Production composition fail-closed tests exist and pass. |
| V09 | The public package boundary contract test exists and passes. |
| V10 | OpenRouter protocol contract tests exist and pass. |
| V11 | Import-graph architecture fitness tests exist and pass. |
| V12 | `go test ./...` passes. |

## Rejected designs

- Big-bang rewrite. Reason: the current gateway behavior and dirty worktree need a strangler migration. Re-open only if characterization cannot isolate a safe seam.
- One shared JSON struct across HTTP, gateway, cache, and providers. Reason: structural DRY would couple protocol changes to business policy. Re-open only with generated adapters and compatibility evidence.
- Routing policy inside Starmap. Reason: catalog evidence and runtime policy have different owners and change rates. Re-open only after a joint Starport and Starmap architecture decision.
- Parallel internal and public connector APIs. Reason: they already drift. Re-open only when a named SDK consumer funds a versioned contract.

## Tasks

### SVA0 Baseline and red verifier

- Problem: the program needs a pinned baseline, fixed verifier, proof root, and fail-before evidence.
- Owning seam and paths: this plan, `scripts/verify-v1-architecture.sh`, and `docs/proof/starport-v1-architecture/`.
- Steps:
  1. Record the branch, commit, dirty paths, package graph, test count, and coverage.
  2. Add the 12-condition verifier with stable condition IDs.
  3. Run the verifier before production changes.
  4. Review the plan for scope, ordering, criteria, and goal completeness.
- Acceptance: the proof file records the exact red summary, `go test ./...`, coverage by package, and plan-review verdict.
- Fail-before: the verifier reports at least one failed condition.
- Verification: `bash scripts/verify-v1-architecture.sh` && `/Users/jack/.agents/skills/technical-writing/scripts/technical-writing lint docs/STARPORT_V1_ARCHITECTURE_PLAN.md --mode developer --format text` && `test "$(rg '^\\| SVA[0-9]+ .*in_progress' docs/STARPORT_V1_ARCHITECTURE_PLAN.md | wc -l | tr -d ' ')" = 1`.

### SVA1 Provider credential storage identity

- Problem: provider-key write and list paths use different durable prefixes.
- Owning seam and paths: `internal/credentials`, `internal/providers/byok`, and provider-credential repository contract fixtures.
- Steps:
  1. Add a failing test that writes a global key and lists it.
  2. Select one canonical prefix and record its schema version.
  3. Use the canonical key for exact reads, scans, writes, and deletes.
  4. Remove the second namespace and all compatibility branches.
- Acceptance: `TestListGlobalKeysReadsCanonicalRecord` and `TestProviderCredentialRepositoryContract` pass without plaintext proof data. No production Go file contains the removed namespace.
- Fail-before: the canonical write followed by list returns no key on the baseline.
- Verification: `go test ./internal/providers/byok ./internal/models ./internal/storage` && `go test -race ./internal/providers/byok`.

### SVA2 Canonical inference and failure seams

- Problem: request, response, stream, usage, tool, and error semantics have multiple owners.
- Owning seam and paths: new `internal/inference` and `internal/failure` packages plus adapters in proxy and connectors.
- Steps:
  1. Define immutable or copy-safe canonical values without transport tags.
  2. Define typed stream events and normalized failures.
  3. Add explicit HTTP and provider conversions.
  4. Migrate one vertical chat path, then embeddings and streams.
- Acceptance: `TestCanonicalInferenceContract` covers text, image, tools, structured output, reasoning, usage, and streams. Failure fixtures retain safe and provider details separately.
- Fail-before: characterization fixtures show the current type conversions and lost fields.
- Verification: `go test ./internal/inference ./internal/failure ./internal/proxy ./internal/providers/connectors` && `go test ./internal/inference -fuzz FuzzCanonicalInference -fuzztime=10s`.

### SVA3 Starmap catalog and routable snapshot

- Problem: catalog facts, dynamic discovery, invalid-model state, and routing availability share mutable global ownership.
- Owning seam and paths: `go.mod`, new `internal/catalog`, Starmap client integration, registry adapters, and legacy `pkg/catalog` callers.
- Steps:
  1. Upgrade the Go floor and add a tagged Starmap release.
  2. Expose one immutable catalog snapshot with its generation identity.
  3. Model runtime adapter availability outside the catalog snapshot.
  4. Derive one routable snapshot for discovery and planning.
  5. Remove global catalog mutation from request paths.
- Acceptance: `TestRoutableSnapshotGenerationConsistency`, `TestCatalogActivationIsAtomic`, and `TestUnavailableAdapterIsNotAdvertised` pass.
- Fail-before: baseline discovery and routing can read separate mutable catalog state.
- Verification: `go test ./internal/catalog ./internal/registry ./internal/proxy ./internal/router` && `go test -race ./internal/catalog ./internal/registry`.

### SVA4 Pure route planner

- Problem: routing policy currently selects connectors and executes provider requests.
- Owning seam and paths: new `internal/routing` package and adapters from the current router API.
- Steps:
  1. Define request requirements, route policy, candidate rejection, route plan, and selection evidence.
  2. Make planning deterministic for fixed inputs.
  3. Move capability, tenant, provider, health, latency, cost, and affinity policy into injected snapshots.
  4. Keep connectors and network work outside the planner.
- Acceptance: `TestRoutePlannerContract` and `TestRoutePlannerDeterministic` cover order, fallback, capabilities, tenant policy, cost, latency, affinity, and no-candidate failures.
- Fail-before: an import test shows that the current router imports connectors and executes provider calls.
- Verification: `go test ./internal/routing` && `go test ./internal/routing -fuzz FuzzRoutePlanner -fuzztime=10s` && `go test ./internal/routing -bench BenchmarkRoutePlanner -run '^$'` && `go test ./internal/architecture -run '^TestImportGraphArchitecture$'`.

### SVA5 Attempt executor and runtime availability

- Problem: retries, fallback, streaming, health, and circuit state have several owners.
- Owning seam and paths: new `internal/execution` and `internal/availability` packages, connector retry code, proxy streaming path, and HTTP transport breaker.
- Steps:
  1. Define the attempt state machine and evidence record.
  2. Enforce one request attempt and time budget.
  3. Execute one route plan for streaming and non-streaming requests.
  4. Normalize failures and update one offering-level availability owner.
  5. Remove competing retry and circuit policy.
- Acceptance: `TestAttemptStateAndRetryBudgetContract` covers success, retry, fallback, cancellation, timeout, first-byte boundary, stream failure, and recovery with a fake clock.
- Fail-before: baseline evidence shows independent connector retry, router fallback delay, and circuit state.
- Verification: `go test ./internal/execution ./internal/availability ./internal/providers/connectors ./internal/proxy` && `go test -race ./internal/execution ./internal/availability ./internal/providers/connectors ./internal/proxy`.

### SVA6 Concept repositories

- Problem: controllers and business services use raw KV operations and several packages create durable keys.
- Owning seam and paths: new identity, credentials, rate-limit, and preset repository seams over `internal/storage` adapters.
- Steps:
  1. Define narrow repository contracts and versioned records.
  2. Centralize key schemas with each concept.
  3. Add schema-conformance fixtures and compare-and-swap rules.
  4. Migrate controllers and services away from raw `KVStore`.
- Acceptance: repository contract suites pass against memory and Badger. Valkey runs when `TEST_VALKEY_URL` exists and otherwise reports `UNVERIFIED`.
- Fail-before: import evidence shows controllers, cache, and BYOK own raw keys and serialization.
- Verification: `go test ./internal/identity ./internal/credentials ./internal/ratelimit ./internal/presets ./internal/storage` && `go test -race ./internal/identity ./internal/credentials ./internal/ratelimit`.

### SVA7 Tenant-safe response cache

- Problem: response cache identity does not preserve all response-changing or tenant semantics.
- Owning seam and paths: new `internal/responsecache`, inference result serialization, and current proxy/cache migration adapters.
- Steps:
  1. Define cache eligibility and semantic identity.
  2. Include tenant, policy, model chain, provider constraints, tools, reasoning, and catalog generation.
  3. Store versioned canonical inference results.
  4. Reconstruct stream events from the canonical result.
  5. Keep unsafe request shapes uncached.
- Acceptance: `TestSemanticKeyAndTenantIsolationContract` proves pairwise field sensitivity, tenant isolation, generation invalidation, and stream reconstruction.
- Fail-before: two baseline requests that differ only by provider policy produce the same key.
- Verification: `go test ./internal/responsecache ./internal/cache ./internal/proxy` && `go test -race ./internal/responsecache ./internal/cache ./internal/proxy` && `go test ./internal/responsecache -fuzz FuzzSemanticKey -fuzztime=10s`.

### SVA8 Composition, configuration, and lifecycle

- Problem: server and registry constructors create business services, read environment values, start work, and select mocks.
- Owning seam and paths: `internal/app`, `internal/config`, `internal/server`, registry replacement, and explicit test kit.
- Steps:
  1. Parse external configuration once.
  2. Construct every dependency in app bootstrap.
  3. Inject ready use-case ports into HTTP adapters.
  4. Add explicit start and close ownership for background components.
  5. Move mocks to an explicit test builder.
- Acceptance: `TestProductionCompositionFailsClosed` covers missing storage, catalog, credentials, and providers. Lifecycle tests prove reverse-order close and cancellation.
- Fail-before: current server and registry select mock dependencies without an explicit test mode.
- Verification: `go test ./internal/app ./internal/config ./internal/server ./internal/registry` && `go test -race ./internal/app ./internal/server ./internal/registry`.

### SVA9 Protocol adapters and public boundary

- Problem: HTTP DTOs, application types, provider types, and an unused public connector API overlap.
- Owning seam and paths: new `internal/httpapi/openai` and `internal/httpapi/openrouter`, server routes, DTOs, `pkg/providers`, `pkg/models`, and remaining legacy catalog packages.
- Steps:
  1. Decode each protocol into canonical inference values.
  2. Encode results, failures, and SSE events in the selected wire dialect.
  3. Preserve exact route and field compatibility through fixtures.
  4. Remove unused public APIs and duplicate internal types under the binary-first decision.
  5. Add architecture import rules.
- Acceptance: `TestOpenRouterProtocolContract`, `TestOpenAIProtocolContract`, and `TestPublicPackageBoundary` pass. Raw HTTP and SDK smoke tests change only base URL and API key.
- Fail-before: current route, error, response-format, stream-error, and public connector fixtures show drift.
- Verification: `go test ./internal/httpapi/... ./internal/server ./internal/architecture` && `bash scripts/smoke-openrouter-sdks.sh`.

### SVA10 Final gates and documentation

- Problem: the architecture needs one closeout gate with traceable evidence.
- Owning seam and paths: verifier, proof root, architecture docs, operator guide, CI, and benchmarks.
- Steps:
  1. Run every deterministic and optional live gate separately.
  2. Run race, fuzz, fault, security, and benchmark checks.
  3. Update architecture and operator documentation.
  4. Review the final diff and unresolved findings.
- Acceptance: the verifier reports `Summary: 12 passed, 0 failed`. Required tests and race checks pass. Optional external checks are green or `UNVERIFIED`, never implied green.
- Fail-before: any verifier failure or unresolved blocker prevents closeout.
- Verification: `bash scripts/verify-v1-architecture.sh` && `go test ./...` && `go test -race ./internal/inference ./internal/catalog ./internal/routing ./internal/execution ./internal/availability ./internal/responsecache ./internal/app ./internal/server` && `go vet ./...` && `make lint` && `make build` && `docker build .` && `bash scripts/smoke-openrouter-sdks.sh`.

### SVA12 Hardcoded catalog-fact audit

- Problem: the architecture verifier checks package direction but does not prove that Starmap owns provider and model facts at runtime.
- Owning seam and paths: `scripts/verify-starmap-ownership.sh`, this findings ledger, and `docs/proof/starport-v1-architecture/sva12-hardcoded-catalog-facts.md`.
- Steps:
  1. Inventory each production provider ID, model family, endpoint, auth rule, and credential field. Include capabilities, prices, limits, lifecycle, and availability.
  2. Classify each value as a Starmap fact, Starport adapter implementation, Starport policy, operator configuration, or runtime observation.
  3. Add fixed red checks for provider coverage, auth ownership, snapshot discovery, exact IDs, catalog pricing, and model-name heuristics.
  4. Record fail-before evidence and route every actionable finding to SVA13-SVA16.
- Acceptance: every inventoried value has one owner. `verify-starmap-ownership.sh` emits a stable roster and fails on the captured baseline. No production behavior changes.
- Fail-before: the verifier detects missing providers, Google query authentication, embedding assumptions, Anthropic ID changes, fake Azure models, and sample prices.
- Verification: `! bash scripts/verify-starmap-ownership.sh` && `/Users/jack/.agents/skills/technical-writing/scripts/technical-writing lint docs/STARPORT_V1_ARCHITECTURE_PLAN.md docs/proof/starport-v1-architecture/sva12-hardcoded-catalog-facts.md --mode developer --format text`.

### SVA13 Starmap catalog acquisition and provider data

- Problem: Starmap owns API-key loading, Vertex ADC, provider clients, live acquisition, reconciliation, and atomic publication. Cloud-auth selection still depends on the Google endpoint type. Three Starport adapters lack provider records.
- Owning seam and paths: `/Users/jack/src/github.com/agentstation/starmap/pkg/catalogs`, Starmap auth and acquisition, embedded records, generated projections, and contract tests.
- Steps:
  1. Consolidate the current API-key and Google ADC resolution behind one typed catalog-acquisition auth contract that never serializes secret values.
  2. Select API-key, Google ADC, Azure, and future cloud chains from catalog auth metadata. Do not use provider IDs or endpoint conditionals.
  3. Keep the current provider-client factory, acquisition syncer, reconciliation, and publication as the only model-update path. Accept only acquisition credentials.
  4. Add inference-operation endpoint and provider service-capability facts while preserving status-page and governance data in the same validated provider record.
  5. Add authoritative Mistral, Azure OpenAI, and Ollama provider descriptors. Do not fabricate Azure deployments or local Ollama offerings.
  6. Represent provider-scoped prompt-cache and operation support on offerings when it differs from intrinsic model capability.
  7. Regenerate projections and documentation through repository-owned tools.
- Acceptance: `TestCatalogAcquisitionAuthContract`, `TestCloudCredentialChainSelection`, and `TestAcquisitionCredentialsNeverSerialize` cover API keys and cloud chains. They also cover workload identity and redaction. `TestProviderInferenceContract`, `TestProviderOfferingServiceCapabilities`, and embedded validation cover endpoints, status pages, acquisition, and stable provider facts. Tests contain no fabricated tenant records.
- Fail-before: Starmap's auth checker branches on the Google Cloud endpoint type and lookup for Mistral, Azure OpenAI, and Ollama fails.
- Verification: `cd /Users/jack/src/github.com/agentstation/starmap && go test ./pkg/catalogs ./pkg/sources ./acquisition ./internal/auth/... ./internal/providers/... ./internal/embedded/... -race && make docs-check && make catalog-cross-reference-check`.

### SVA14 Catalog-backed provider activation and credentials

- Problem: Starport switches and config defaults jointly own provider membership, credentials, auth placement, and endpoints.
- Owning seam and paths: `internal/app`, `internal/catalog`, `internal/config`, `internal/providers/byok`, `internal/providers/connectors`, and the Starmap module boundary.
- Steps:
  1. Create one typed adapter registry keyed by `catalogs.ProviderID`. Keep implementation dispatch there and remove separate provider lists.
  2. Derive active providers from the catalog, compiled adapters, and valid operator configuration. Fail startup if a provider lacks either contract.
  3. Put every inference credential field, local validation rule, request-signing behavior, and optional network probe in the typed Starport adapter registry.
  4. Resolve provider and offering endpoint facts from Starmap while keeping inference authentication, encrypted values, and operator endpoint overrides in Starport.
  5. Compose Starmap acquisition and refresh with a distinct acquisition credential source. Remove connector refresh and forbid access to inference credentials.
  6. Materialize valid Azure and Ollama observations as Starmap definitions and offerings before activation. Remove Bedrock validation until an adapter exists.
- Acceptance: `TestActiveProviderIntersection`, `TestConfiguredProviderMissingCatalogFailsStartup`, `TestAdapterRegistryDrivesInferenceCredentialValidation`, `TestGoogleAPIKeyUsesInferenceHeader`, `TestAuthPlanesAreIsolated`, `TestStarmapAcquisitionPublishesRefresh`, and `TestTenantOfferingsEnterCatalogGeneration` pass. Adding a synthetic provider descriptor needs one adapter registration and no second provider switch. No URL contains a secret.
- Fail-before: Mistral, Azure OpenAI, and Ollama register but produce no routes. Bedrock credentials lack an adapter. Google puts its key in URLs.
- Verification: `go test ./internal/app ./internal/catalog ./internal/config ./internal/providers/byok ./internal/providers/connectors` && `go test -race ./internal/app ./internal/catalog ./internal/providers/byok ./internal/providers/connectors`.

### SVA15 Snapshot-only model and provider facts

- Problem: request paths and adapters infer model behavior and mutate exact identifiers. They also fabricate availability and use provider-wide fact lists.
- Owning seam and paths: `internal/catalog`, `internal/proxy`, `internal/routing`, `internal/providers/connectors`, and protocol contract fixtures.
- Steps:
  1. Project provider, model, and endpoint discovery from one routable snapshot without request-time provider calls or connector model caches.
  2. Select chat and embedding routes from definition and offering capabilities intersected with adapter operation support.
  3. Pass exact `ProviderModelID` values unchanged and use offering endpoint facts instead of name-family publisher heuristics.
  4. Replace static health IDs, Gemini filtering, fake Azure models, prompt-cache lists, provider metadata, model-name sorting, and sample prices with projections.
  5. Delete dormant billing when v1 has no consumer. Otherwise require an exact offering, price unit, and effective price.
- Acceptance: `TestSnapshotOnlyDiscovery`, `TestEmbeddingRequiresCatalogAndAdapterCapability`, `TestExactProviderModelIDIsOpaque`, `TestOfferingEndpointSelectsProtocol`, `TestOfferingCacheCapability`, and `TestOfferingPriceHasNoFallback` pass. Production connector and proxy code contains no recognizable model ID or model-family dispatch.
- Fail-before: endpoint discovery calls all connectors and returns `available: true`. Embeddings assume support. Adapters mutate IDs and fabricate deployments. Billing has a default.
- Verification: `go test ./internal/catalog ./internal/proxy ./internal/routing ./internal/providers/connectors ./internal/httpapi/...` && `go test -race ./internal/catalog ./internal/proxy ./internal/routing ./internal/providers/connectors`.

### SVA16 Cross-repository final ownership gates

- Problem: final verification must prove both repositories and the OpenRouter wire boundary after the new Starmap contract lands.
- Owning seam and paths: both repository test suites, both ownership verifiers, docs, SDK smoke fixtures, and `docs/proof/starport-v1-architecture/sva16-starmap-ownership-final.md`.
- Steps:
  1. Run the Starmap schema, catalog-acquisition auth, validation, race, documentation, generation, and diff gates.
  2. Run the Starport ownership, architecture, full, race, fuzz, security, build, and SDK smoke gates.
  3. Mutation-test a synthetic provider, endpoint, capability, model ID, and price. Prove that changes flow without new Starport conditionals.
  4. Mutate one Starport inference credential descriptor. Review SVF-010 through SVF-020 and record a terminal disposition for each finding.
- Acceptance: both repository suites and both verifiers pass. The mutation and auth-plane isolation contracts pass. No secret enters output. Protocol fixtures remain compatible. All new findings are terminal.
- Fail-before: the pre-change ownership verifier is red while the existing architecture verifier remains green.
- Verification: `cd /Users/jack/src/github.com/agentstation/starmap && make verify` && `cd /Users/jack/src/github.com/agentstation/starport && bash scripts/verify-starmap-ownership.sh && bash scripts/verify-v1-architecture.sh && go test ./... && go test -race ./internal/catalog ./internal/proxy ./internal/routing ./internal/providers/connectors ./internal/app ./internal/server && go vet ./... && make lint && make build && bash scripts/smoke-openrouter-sdks.sh`.

### SVA11 Cleanup after merge

- Problem: a merged plan must not remain as a stale control plane.
- Owning seam and paths: this plan, `docs/proof/starport-v1-architecture/`, and `docs/README.md`.
- Steps:
  1. Confirm that the final pull request merged.
  2. Delete this plan, its proof root, and its index entry.
  3. Route residual work to its named owner.
- Acceptance: the repository has no active reference to this plan, or a repository-approved archive owns the terminal record.
- Fail-before: not applicable because the merge triggers this task.
- Verification: `! rg -n 'starport-v1-architecture|STARPORT_V1_ARCHITECTURE_PLAN' docs README.md`.

## Goal

```text
Execute docs/STARPORT_V1_ARCHITECTURE_PLAN.md to completion. This is a
whole-plan goal, not a single-task goal. Read the plan fully, then read:
AGENTS.md, docs/ARCHITECTURE.md, docs/ARCHITECTURE_CONTROL_PLANE.md,
docs/PLAN.md, /Users/jack/src/github.com/agentstation/starmap/AGENTS.md,
/Users/jack/src/github.com/agentstation/starmap/docs/STARPORT_CATALOG_CONTROL_PLANE.md,
and /Users/jack/src/github.com/agentstation/starmap/docs/REST_API.md. Work in
/Users/jack/src/github.com/agentstation/starport on branch master and
/Users/jack/src/github.com/agentstation/starmap on branch main. Chat history
is not progress state. Resume from the status ledger, the execution log, and
both repositories' git state. If compaction happens, continue from the plan
and git state rather than restarting. Loop: keep one task in_progress, implement at
the owning seam, capture fail-before evidence, run the verification commands,
write the proof file, append the execution log, mark the task terminal with
evidence, then advance to the next task. Decide rather than ask. Mark a wrong
or already-satisfied task no-action with a one-line reason. Record a blocker
and continue with the next eligible task. Binding constraints: preserve all
12 invariants, preserve unrelated dirty work, use Starmap only for catalog
facts and catalog-acquisition authentication, keep gateway and inference
authentication plus routing policy in Starport, isolate the two credential
planes, do not weaken tests, do not expose secrets, and do not expand into
the listed non-goals. Commit policy: do not
create commits, branches, pushes, pull requests, deployments, or releases
without explicit owner approval. Keep verified changes in the worktree and
record exact evidence. Stop only at a valid stop state from the plans skill.
Before stopping, update the ledger and log, and record the exact next action
in the status line. The goal is met when SVA0 through SVA10 and SVA12 through
SVA16 are terminal, all in-scope findings are terminal, both ownership and
architecture verifiers are green, required deterministic and race gates pass,
and SVA11 waits only for the merge of the final owner-approved pull request.
```

## Execution log

Append rows at the end. This section stays last.

| Date | Item | Action | Evidence |
|---|---|---|---|
| 2026-08-03 | meta | Created and activated the Starport v1 architecture plan from the approved architecture assessment. | Baseline `bf6b09e11369`. Plan creation changed no production behavior. |
| 2026-08-03 | SVA0 | Captured the baseline, reviewed the plan, and advanced SVA1. | `docs/proof/starport-v1-architecture/sva0-baseline.md`; verifier `Summary: 1 passed, 11 failed`; 23 packages; 401 top-level tests; zero plan-lint diagnostics. |
| 2026-08-03 | SVA1 | Unified provider-credential key identity, then amended it at owner direction to remove the unreleased compatibility namespace. Advanced SVA2. | `docs/proof/starport-v1-architecture/sva1-provider-credential-storage.md`; canonical lifecycle and race gates passed; verifier `Summary: 7 passed, 5 failed`; V06 remains green. |
| 2026-08-03 | SVA2 | Routed chat, embeddings, streams, provider failures, and HTTP failures through canonical seams. Advanced SVA3. | `docs/proof/starport-v1-architecture/sva2-canonical-inference.md`; 10-second fuzz gate ran 4,780,319 executions; race and full Go gates passed; verifier `Summary: 3 passed, 9 failed`. |
| 2026-08-03 | SVA3 | Integrated Starmap, separated runtime adapter availability, published atomic routable snapshots, and advanced SVA4. | `docs/proof/starport-v1-architecture/sva3-starmap-catalog.md`; catalog and registry race gate passed; verifier `Summary: 5 passed, 7 failed`. |
| 2026-08-03 | SVA4 | Extracted deterministic pure route planning, added import-graph fitness, integrated the catalog-backed adapter, and advanced SVA5. | `docs/proof/starport-v1-architecture/sva4-route-planner.md`; fuzz, benchmark, race, full-suite, and architecture gates passed; verifier `Summary: 7 passed, 5 failed`. |
| 2026-08-03 | SVA5 | Unified streaming and non-streaming attempts under one executor, added one offering availability owner, removed competing retry and circuit policy, and advanced SVA6. | `docs/proof/starport-v1-architecture/sva5-attempt-execution.md`; package, full-suite, race, vet, writing, and architecture gates passed; verifier `Summary: 8 passed, 4 failed`. |
| 2026-08-03 | SVA6 | Added four versioned concept repositories, removed old durable schemas and duplicate cache ownership, migrated identity and credential consumers, and advanced SVA7. | `docs/proof/starport-v1-architecture/sva6-concept-repositories.md`; memory, Badger, race, vet, full-suite, writing, and architecture gates passed; Valkey `UNVERIFIED`; verifier `Summary: 8 passed, 4 failed`. |
| 2026-08-03 | SVA7 | Added tenant-safe semantic response identity, versioned canonical records, fail-closed eligibility, and canonical stream replay. Advanced SVA8. | `docs/proof/starport-v1-architecture/sva7-tenant-safe-response-cache.md`; package, corruption, partial-stream, race, fuzz, vet, full-suite, writing, and architecture gates passed; verifier `Summary: 9 passed, 3 failed`. |
| 2026-08-03 | SVA8 | Centralized production composition and lifecycle, made registry start explicit, removed implicit mocks and unreleased compatibility paths, and advanced SVA9. | `docs/proof/starport-v1-architecture/sva8-composition-lifecycle.md`; focused, race, lifecycle, full-suite, vet, writing, and architecture gates passed; verifier `Summary: 10 passed, 2 failed`. |
| 2026-08-03 | SVA9 | Separated OpenAI and OpenRouter wire adapters, removed unused public packages and duplicate catalog facts, made provider IDs exact, added raw and optional SDK smoke fixtures, and advanced SVA10. | `docs/proof/starport-v1-architecture/sva9-protocol-public-boundary.md`; protocol, architecture, focused, full-suite, writing, smoke, and diff gates passed; verifier `Summary: 12 passed, 0 failed`; optional SDKs `UNVERIFIED`. |
| 2026-08-03 | SVA10 | Closed production bootstrap, protocol routing, deployment, documentation, lint, and dependency-security gaps. SVA11 now waits only for an owner-approved merge. | `docs/proof/starport-v1-architecture/sva10-final-gates.md`; verifier `Summary: 12 passed, 0 failed`; full, race, fuzz, fault, security, zero-reachable-vulnerability, benchmark, build, container, Compose, Valkey, raw smoke, writing, and diff gates passed; optional SDKs `UNVERIFIED`. |
| 2026-08-03 | meta | Reopened the active plan after a hardcoded-fact audit found that the prior verifier could pass while Starport duplicated or bypassed Starmap facts. The owner clarified that Starmap catalog-acquisition auth and Starport inference auth are isolated concepts. Advanced SVA12. | SVF-010 through SVF-020; focused tests in six Starport packages pass on the defective baseline; no production behavior changed during the re-scope. |
| 2026-08-03 | SVA12 | Classified hardcoded facts, added the red ownership verifier, recorded 13 findings, and advanced SVA13. | `docs/proof/starport-v1-architecture/sva12-hardcoded-catalog-facts.md`; ownership `Summary: 0 passed, 12 failed`; architecture `Summary: 12 passed, 0 failed`; zero writing diagnostics. |
| 2026-08-03 | SVA13 | Added typed acquisition auth and inference-service contracts to Starmap, removed endpoint-driven credential selection, accepted workload identity through the provider SDK chain, added Mistral/Azure/Ollama provider records, regenerated catalog projections and docs, and advanced SVA14. | `docs/proof/starport-v1-architecture/sva13-starmap-provider-contracts.md`; affected race suites pass; 14 providers, 104 authors, 611 model definitions, and cross-references validate; vet and pinned lint pass; ownership O11 passes. |
| 2026-08-03 | SVA14 | Added one typed adapter registry, catalog-backed provider activation, durable Starmap acquisition publication, tenant offering activation, and isolated acquisition and inference authentication. Advanced SVA15. | `docs/proof/starport-v1-architecture/sva14-catalog-backed-provider-activation.md`; full Starport suite, focused deterministic and race gates, Starmap acquisition gates, vet, diff, and writing gates pass; ownership O01-O04 and O11 pass. |
| 2026-08-03 | SVA15 | Removed connector-owned discovery and fabricated provider facts, projected exact offering capabilities and endpoints into attempts, preserved opaque provider model IDs, and advanced SVA16. | `docs/proof/starport-v1-architecture/sva15-snapshot-only-provider-facts.md`; both full Go suites, focused race gates, vet, diff, and ownership `Summary: 12 passed, 0 failed` pass. |
| 2026-08-03 | SVA16 | Removed the last static provider URLs and transport guesses, added author-specific and stream-specific Starmap endpoint facts, bound runtime endpoint variables generically, closed all new findings, and reached the owner-approval stop state. | `docs/proof/starport-v1-architecture/sva16-starmap-ownership-final.md`; both verifiers report `Summary: 12 passed, 0 failed`; Starmap `make verify`, Starport full and focused race suites, 8,396,231 fuzz executions, vet, zero-issue lint, build, raw smoke, zero-reachable-vulnerability, writing, and diff gates pass; optional SDKs `UNVERIFIED`. |
| 2026-08-03 | SVA16 | Reopened closeout after a requirement-by-requirement audit found stale connector discovery documentation, an unused registry model-prefix resolver, a multi-generation model-list read, and a V01 false pass for the local Starmap replacement. | SVF-021. No commit, publication, or external state change occurred. |
| 2026-08-03 | SVA16 | Repaired every local completion-audit defect and hardened O05, O08, and V01. Stopped before the external publication boundary. | Ownership `Summary: 12 passed, 0 failed`; architecture `Summary: 11 passed, 1 failed` at V01; both full suites, focused race, vet, zero-issue lint, build, writing, and diff gates pass. Owner approval is required for a Starmap tag and Starport module pin. |
| 2026-08-03 | SVA16 | Resumed publication after the owner authorized a Starmap pull request, merge, v0.3.0 release, and Starport update. | SVA16 is `in_progress`; publication will use the repository release workflow after the Starmap change merges to `main`. |
| 2026-08-04 | SVA16 | Published the provider contract, recovered the immutable release with scoped Homebrew credentials, pinned Starport to v0.3.0, and completed every final gate. | Starmap PRs #64 and #65; release and recovery run `30881177476`; both terminal verifiers 12/12; full, race, fuzz, vulnerability, vet, lint, build, and raw protocol gates pass. SVA11 now waits for the final Starport pull request merge. |
