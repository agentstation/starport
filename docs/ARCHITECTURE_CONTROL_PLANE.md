# Starport Architecture Control Plane

Last updated: 2026-08-03

Status: historical. [Architecture](ARCHITECTURE.md) defines the canonical v1
design. Do not use this ledger's goal prompt or target architecture to resume
current work.

This document is the durable control plane for the Starport architecture hardening effort. It is written so a future agent can continue after context compaction without relying on chat history.

## Goal Prompt

Use this exact goal when resuming the work:

```text
/goal Execute the Starport architecture hardening control plane end to end. Work from docs/ARCHITECTURE_CONTROL_PLANE.md as the source of truth. Keep the ledger and execution log current. Preserve unrelated user changes. Remediate lifecycle ownership, concurrency, connector transport reliability, HTTP middleware safety, security posture, rate limiting, routing contracts, package seams, and testability until go test ./... passes and any selected race/contract tests are green, or record a concrete blocker with exact evidence.
```

## Operating Rules

- Treat this file as the working ledger for this architecture hardening program.
- Do not use `docs/TASKS.md` unless a formal Starport task ID is assigned.
- Preserve unrelated dirty work. Inspect before editing any file that is already modified.
- Prefer small, verifiable slices over broad rewrites.
- Update the ledger before switching phases, after each verification run, and when a blocker is discovered.
- Every completed item must cite verification evidence in the execution log.
- Architecture changes should increase module depth, interface leverage, locality, and testability.
- Public `pkg/` packages must either be intentional stable interfaces or be moved/hidden behind `internal/` seams.

## Current Work Snapshot

- Branch recorded by this historical plan: `master`
- Active work: architecture hardening after a full architecture review.
- Current verification: final `go test ./...` and selected race suite pass after P7 documentation closeout.
- Original baseline failure is preserved in P0-E3 for historical context.
- Known remaining architectural themes:
  - Future product work remains for content filtering, preset endpoints, observability, analytics, webhooks, and enterprise SSO/RBAC.
- Important existing remediation already present in the worktree:
  - `CLAUDE.md` is a symlink to `AGENTS.md`.
  - Runtime storage injection was improved so production startup can use configured Badger storage.
  - Provider-key management no longer silently requires a mock-only setup; it is disabled when the master key is absent.
  - Google provider slug normalization and proxy provider-preference forwarding were partially remediated.
- Dirty worktree warning:
  - Some dirty files existed before this control plane was created.
  - Do not revert unrelated changes unless explicitly requested.

## Architecture Target

Starport should be structured as a high-reliability Go gateway with these module seams:

1. `cmd/starport`: CLI and composition root only.
2. `internal/app`: lifecycle owner for storage, cache, registry, background workers, and HTTP server.
3. `internal/server` or future `internal/httpapi`: HTTP transport, parsing, middleware, and response mapping only.
4. `internal/proxy` or future `internal/gateway`: application use cases for chat, streaming chat, embeddings, model listing, and provider listing.
5. `internal/router`: routing policy and fallback decisions behind injected selector, health, cost, and affinity interfaces.
6. `internal/providers`: concrete provider adapters behind one connector contract and one shared HTTP transport policy.
7. `internal/storage`: storage adapters plus shared contract tests.
8. `pkg/*`: stable public interfaces only.

## Status Ledger

Status values: `todo`, `in_progress`, `blocked`, `done`.

| ID | Phase | Status | Owner | Scope | Acceptance Criteria | Verification |
| --- | --- | --- | --- | --- | --- | --- |
| P0 | Control plane and baseline | done | Codex | Create durable plan, review it, set active goal, capture baseline test state. | This file exists; goal prompt is recorded; active goal is set; baseline `go test ./...` state is logged; no unrelated changes reverted. | Execution log entries P0-E1 through P0-E4. |
| P1 | Connector transport reliability | done | Codex | Make all connectors consistently use the shared HTTP client semantics for request execution, timeouts, retries, and pooling. | `go test ./internal/providers/connectors` passes; provider tests prove requests hit test servers where expected; timeout test fails fast; connection pooling test respects configured concurrency; connector constructors remain idiomatic `NewX` or `Open` style. | Execution log entries P1-E1 through P1-E3. |
| P2 | Lifecycle ownership and concurrency | done | Codex | Make app/server/registry/cache ownership explicit and remove unsafe or unmanaged goroutine patterns. | App is the single owner of shared dependency shutdown; server shutdown only drains HTTP; registry health checks do not hold locks while doing network work; registration validation is context-bound or explicitly started/stopped; no recursive RWMutex usage. | Execution log entries P2-E1 through P2-E4. |
| P3 | HTTP middleware, security, and rate limiting | done | Codex | Make request middleware production-safe. | Global timeout middleware no longer races with streaming `ResponseWriter`; streaming routes are not wrapped by unsafe request timeout; API keys are accepted only through headers; rate limiting middleware uses existing cache/storage primitive and returns OpenAI-compatible rate-limit errors/headers. | Execution log entries P3-E1 through P3-E5. |
| P4 | Routing contracts and streaming fallback | done | Codex | Complete routing inputs and make streaming behavior explicit. | API-key provider restrictions reach `router.Request.APIKeyConfig`; route preference semantics are either implemented or rejected during validation; metadata used by routing is populated or documented as unavailable; streaming fallback behavior is implemented before first byte or explicitly encoded as unsupported after stream start. | Execution log entries P4-E1 through P4-E4. |
| P5 | Package seams and domain model | done | Codex | Improve module locality and clarify public interfaces. | `internal/server/dto` no longer depends directly on proxy internals unless intentionally justified; provider/model ID parsing and normalization live in one module; `pkg/` packages are documented as public API or moved behind `internal/`; global catalog mutable state has reset/injection seams for tests. | Execution log entries P5-E1 through P5-E4. |
| P6 | Testability and contract tests | done | Codex | Add tests that lock down architecture-level behavior. | KVStore contract tests run against mock/Badger and Valkey when available; connector contract tests share a fake provider harness; lifecycle tests catch double-close regressions; selected `-race` runs are green or documented with external blockers. | Execution log entries P6-E1 through P6-E4. |
| P7 | Final verification and documentation | done | Codex | Close the architecture program with green verification and updated docs. | `go test ./...` passes; focused race/contract tests pass or blockers are documented; `docs/ARCHITECTURE.md` no longer contains stale claims contradicted by code; ledger is fully closed. | Execution log entries P7-E1 through P7-E4. |

## Detailed Acceptance Criteria

### P0: Control Plane and Baseline

- `docs/ARCHITECTURE_CONTROL_PLANE.md` exists and contains:
  - active goal prompt;
  - operating rules;
  - current work snapshot;
  - phase ledger;
  - execution log;
  - per-phase acceptance criteria.
- A baseline test run is recorded exactly, including failing packages and failure themes.
- The plan is reviewed before implementation proceeds.

### P1: Connector Transport Reliability

- Every provider connector has a clear transport path:
  - shared retry/timeout/pooling policy is used where HTTP calls happen;
  - providers that intentionally return static model lists are documented and tested that way;
  - health checks target endpoints that test servers and real providers can satisfy predictably.
- Tests must distinguish:
  - "static list, no request expected";
  - "dynamic list, request expected";
  - "request path uses shared client";
  - "timeout is enforced";
  - "pooling or concurrency limit is enforced at the right layer."
- No connector should hide network failures behind silent static fallbacks unless that is explicit in the connector contract.

### P2: Lifecycle Ownership and Concurrency

- `App` owns shared dependency lifecycle.
- `Server` owns only the HTTP listener and route tree.
- `Registry` should not perform network I/O while holding its main map lock.
- Background tasks must have:
  - a parent context;
  - a bounded lifetime;
  - shutdown coordination;
  - test coverage for cancellation or close.
- Constructors should not start hidden long-lived work unless their names and docs make that explicit.

### P3: HTTP Middleware, Security, and Rate Limiting

- Unsafe goroutine-per-request timeout wrapper is removed or constrained to non-streaming routes with safe semantics.
- Streaming requests use provider/request contexts and server-level read/write/idle timeouts rather than racing writes.
- API-key extraction rejects query parameters.
- Rate limiting:
  - keys by API key ID or tenant-ready identity, not raw secret;
  - uses atomic storage operations;
  - returns 429 with compatible JSON error;
  - includes useful rate-limit headers when available;
  - has deterministic allow/deny tests.

### P4: Routing Contracts and Streaming Fallback

- Proxy creates a complete `router.Request` from HTTP/application inputs.
- Provider preferences, API-key restrictions, request metadata, and route mode are consistently represented.
- Route modes are not accepted unless implemented.
- Streaming fallback policy is explicit:
  - fallback before the first chunk is allowed;
  - fallback after bytes are sent is not transparent and must surface a stream error or terminate according to documented API semantics.

### P5: Package Seams and Domain Model

- Transport DTO parsing should convert into application request types at a clear seam.
- Model ID handling should be centralized and tested:
  - provider extraction;
  - canonical provider slug normalization;
  - legacy alias support;
  - provider-specific stripped model names.
- Global mutable model/catalog state must be resettable or injectable in tests.
- Public `pkg/` packages must have a short README or doc comment stating whether they are stable external API.

### P6: Testability and Contract Tests

- Add shared test harnesses where repeated behavior matters:
  - connector HTTP contract;
  - storage KV contract;
  - router selection/fallback contract;
  - lifecycle close/cancel contract.
- Run at minimum:
  - `go test ./...`;
  - focused package tests for each changed module;
  - selected `go test -race` for concurrency-heavy packages after P2/P3.

### P7: Final Verification and Documentation

- Update stale architecture claims in `docs/ARCHITECTURE.md`.
- Ensure `docs/README.md` links this control plane if it remains useful.
- Close every ledger item as `done` or `blocked` with evidence.
- Final summary must separate:
  - implementation completed;
  - tests passed;
  - tests blocked or intentionally skipped;
  - residual architectural risks.

## Execution Log

| Entry | Timestamp | Phase | Action | Evidence | Next |
| --- | --- | --- | --- | --- | --- |
| P0-E1 | 2026-07-08 | P0 | Architecture review completed and converted into this control plane. | Review identified connector transport failures, unsafe timeout middleware, split lifecycle ownership, registry lock/concurrency issues, missing rate-limit middleware, query-string API-key risk, incomplete routing/streaming contracts, and package seam issues. | Write and review plan. |
| P0-E2 | 2026-07-08 | P0 | Active goal created for end-to-end architecture hardening. | Goal objective: execute this control plane until `go test ./...` passes or concrete blockers are recorded. | Continue P0 and begin P1. |
| P0-E3 | 2026-07-08 | P0 | Baseline full test state captured. | `go test ./...` fails in `github.com/agentstation/starport/internal/providers/connectors`; other packages passed in that run. Main failure themes: no requests made through expected shared HTTP client path, pooling max concurrent 20 greater than expected <=10, timeout test expected error but got nil. | Start P1 connector transport reliability. |
| P0-E4 | 2026-07-08 | P0 | Plan reviewed and ledger advanced. | Plan contains goal prompt, operating rules, current work snapshot, ledger, detailed acceptance criteria, execution log, and sequencing rationale. P0 marked done and P1 marked in progress. | Inspect connector transport tests and implementation. |
| P1-E1 | 2026-07-08 | P1 | Added connector HTTP client construction seam. | `internal/providers/connectors/http_client.go` maps `ProviderConfig` timeout and max connections into `pkg/httpclient` settings. Retry and availability policy stay outside the transport. | Run connector transport tests. |
| P1-E2 | 2026-07-08 | P1 | Scoped dynamic model cache and clarified static model providers. | Dynamic model cache keys now include provider and base URL; Groq base URL normalization uses `/openai/v1` consistently; tests distinguish dynamic `Models()` requests from static Azure/Vertex lists. | Run full connector package. |
| P1-E3 | 2026-07-08 | P1 | Verified connector transport remediation. | `go test ./internal/providers/connectors` passed. `go test ./...` passed across the repository after P1 changes. | Begin P2 lifecycle ownership and concurrency. |
| P2-E1 | 2026-07-08 | P2 | Moved registry ownership out of server shutdown. | `Server.Shutdown` now drains only the HTTP server; `TestShutdownDoesNotCloseRegistry` verifies the registry remains usable after server shutdown. | Fix registry lock scope. |
| P2-E2 | 2026-07-08 | P2 | Removed registry lock retention during health/model network work. | Health checks and model fetches now use configured connector snapshots; `TestRegistry_HealthCheckDoesNotBlockRegister` verifies registration is not blocked by an in-flight health check. | Add validation lifecycle context. |
| P2-E3 | 2026-07-08 | P2 | Added validation goroutine lifecycle controls. | Registry now owns a validation context, cancel function, and WaitGroup; `Close` cancels validation before closing connectors. | Verify P2 packages and race run. |
| P2-E4 | 2026-07-08 | P2 | Verified lifecycle and concurrency remediation. | `go test ./internal/app ./internal/server ./internal/registry` passed. `go test ./...` passed. `go test -race ./internal/registry ./internal/server ./internal/app` passed after replacing unsafe timeout middleware. | Continue P3 HTTP middleware, security, and rate limiting. |
| P3-E1 | 2026-07-08 | P3 | Replaced unsafe timeout wrapper. | Custom timeout middleware no longer starts a goroutine or writes concurrently; it delegates to chi's context-based timeout middleware for cooperative handlers. Race run passed for server/app/registry. | Remove query-string API-key support and add rate-limit middleware. |
| P3-E2 | 2026-07-08 | P3 | Removed query-string API-key authentication. | `extractAPIKey` now accepts only `Authorization` and `X-API-Key`; auth tests assert `api_key` and `key` query parameters are rejected as missing credentials. `go test ./internal/server` passed. | Add authenticated rate-limit middleware. |
| P3-E3 | 2026-07-08 | P3 | Added authenticated rate-limit enforcement. | Server config now carries enable/limit/window settings; CLI config maps existing rate-limit/security env config into server config; middleware keys by authenticated API key ID; cache manager is used when available and storage-backed limiter is the fallback; deterministic tests cover allow/deny, disabled mode, missing identity, and storage prefixing. | Verify server/app/cache boundaries. |
| P3-E4 | 2026-07-08 | P3 | Verified focused middleware and config boundaries. | `go test ./cmd/starport ./internal/app ./internal/server` passed. `go test -race ./internal/registry ./internal/server ./internal/app` passed. `go test ./internal/cache ./internal/server/controllers` passed. | Run full repository tests. |
| P3-E5 | 2026-07-08 | P3 | Verified full repository after P3. | `go test ./...` passed across all packages. | Begin P4 routing contracts and streaming fallback. |
| P4-E1 | 2026-07-08 | P4 | Routed API-key model restrictions into router requests. | Chat controller now derives proxy API-key routing config from authenticated API-key context; proxy transforms it into `router.APIKeyConfig`; router filters by `AllowedModels` and `AllowedProviders`. Tests cover controller forwarding, proxy request construction, and router filtering. | Tighten unsupported route mode semantics. |
| P4-E2 | 2026-07-08 | P4 | Rejected unimplemented route modes and populated routing metadata. | Validation now accepts only empty route or `fallback`; `balanced`, `priority`, and `random` fail as unsupported. Proxy routing metadata now includes estimated prompt tokens, tool/vision feature hints, and a stable affinity key from the OpenAI `user` field when present. | Implement streaming start fallback. |
| P4-E3 | 2026-07-08 | P4 | Added pre-response streaming fallback. | Streaming chat now retries fallback candidates only when `ChatStream` fails before returning a stream; once a stream is returned, no transparent fallback is attempted after bytes may have been sent. Test proves a 429 stream-start failure falls back to the second model before returning a stream. | Verify P4 packages. |
| P4-E4 | 2026-07-08 | P4 | Verified routing contract remediation. | `go test ./internal/proxy ./internal/router ./internal/server/controllers` passed. `go test ./...` passed across all packages. | Begin P5 package seams and domain model. |
| P5-E1 | 2026-07-08 | P5 | Reviewed package boundaries and public package intent. | `go list` showed `internal/server/dto` intentionally depends on `internal/proxy`; `pkg/README.md` was stale and did not describe actual public packages. | Add public API documentation and model ID helpers. |
| P5-E2 | 2026-07-08 | P5 | Centralized model ID split and provider normalization helpers. | Added `catalog.SplitModelID` and `catalog.NormalizeProvider`; migrated router, registry, proxy, and cost calculation call sites that duplicated provider/model splitting or Google provider alias mapping. | Add catalog state reset seams. |
| P5-E3 | 2026-07-08 | P5 | Added explicit catalog state reset seams and package-seam docs. | Added catalog reset helpers for dynamic/invalid/global catalog state; catalog tests use helpers instead of direct global mutation; `pkg/README.md` now documents `catalog`, `httpclient`, `models`, and `providers`; DTO package comment justifies the proxy decode seam. | Verify P5 packages and full repository. |
| P5-E4 | 2026-07-08 | P5 | Verified package seam remediation. | `go test ./pkg/catalog ./internal/router ./internal/proxy ./internal/registry ./internal/server/dto` passed. `go list` import-boundary readout recorded DTO/proxy and public `pkg/` imports. `go test ./...` passed. | Begin P6 testability and contract tests. |
| P6-E1 | 2026-07-08 | P6 | Added reusable KVStore contract tests. | `internal/storage/kvstore_contract_test.go` runs the same behavioral contract against mock and Badger by default, and Valkey when `TEST_VALKEY_URL` is set. Contract covers basic, TTL, atomic, batch, and transaction semantics. | Add connector contract test. |
| P6-E2 | 2026-07-08 | P6 | Added reusable connector contract tests. | `internal/providers/connectors/connector_contract_test.go` runs the same connector contract against the mock connector and an OpenAI-compatible fake provider, covering chat, streaming, embeddings, models, health, and close. | Run contract packages. |
| P6-E3 | 2026-07-08 | P6 | Verified contract packages and selected race suite. | `go test ./internal/storage` passed. `go test ./internal/providers/connectors` passed. `go test -race ./internal/storage ./internal/providers/connectors ./internal/registry ./internal/server ./internal/app` passed. | Run full repository verification. |
| P6-E4 | 2026-07-08 | P6 | Verified full repository after contract tests. | `go test ./...` passed across all packages. | Begin P7 final verification and documentation. |
| P7-E1 | 2026-07-08 | P7 | Rewrote stale architecture documentation. | `docs/ARCHITECTURE.md` now reflects current auth, cache, rate-limit, provider, routing, lifecycle, storage, API, and verification state instead of claiming auth/rate-limit/cache are missing or broken. | Link control plane from docs index. |
| P7-E2 | 2026-07-08 | P7 | Linked architecture control plane from documentation index. | `docs/README.md` now includes `ARCHITECTURE_CONTROL_PLANE.md` alongside the architecture doc. | Run stale-claim scan and final verification. |
| P7-E3 | 2026-07-08 | P7 | Verified final docs and test state. | Stale-claim scan over `docs/ARCHITECTURE.md` and `docs/README.md` found no matches for the old broken-auth/no-rate-limit/no-cache claims. `go test ./...` passed. `go test -race ./internal/storage ./internal/providers/connectors ./internal/registry ./internal/server ./internal/app` passed. | Capture final diff/status. |
| P7-E4 | 2026-07-08 | P7 | Captured final repository status. | `git status --short` and `git diff --stat` recorded a dirty worktree containing this pass plus pre-existing unrelated changes. `CLAUDE.md` is a symlink to `AGENTS.md`. All ledger phases P0-P7 are closed as done. | Final response. |

## Plan Review

This plan is executable because each phase has:

- a concrete module seam or reliability target;
- acceptance criteria stated as observable behavior;
- verification commands;
- ledger status;
- a next phase that depends on the previous one only where necessary.

The order is intentional:

1. Connector transport first because the current full test suite is already red there.
2. Lifecycle/concurrency next because it controls safe shutdown and background work.
3. HTTP middleware/security/rate limiting next because these affect production safety at the edge.
4. Routing contracts after transport and middleware because routing depends on both.
5. Package seams after behavior is stable, to avoid refactoring through failing tests.
6. Contract tests and docs close the loop.

Known risk: the worktree is already dirty with a mix of current and pre-existing changes. Every phase must inspect touched files before editing and avoid reverting unrelated work.
