# Starport Task Management & Status

**Single Source of Truth for Task Status**  
Last Updated: 2026-08-10

## 🚀 Current Sprint: Starport v1

### Active Work

| Task | Owner | Status | Plan |
|---|---|---|---|
| Starport developer experience | DX9 | Active; DX7 waits for Apple notarization input | [Developer experience plan](plans/starport-developer-experience-plan.html) |

### Recently Completed

| Task | Team | PR | Completion Date | Notes |
|------|------|-----|-----------------|-------|
| SPR3 | Release | #75 | 2026-08-09 | Stored compact terminal proof and removed the completed release control plane |
| SPR2 | Release | Release workflow | 2026-08-09 | Published immutable `v1.0.0` with 13 verified assets and a public, attested, two-platform GHCR image at `sha256:f4230687fdf664022e4be80031c4145ff2eb795ff200489216ea76ba4b64bc24` |
| SPR1 | Release | #73 | 2026-08-09 | Merged the complete v1 release candidate after all 10 CI jobs passed, including cross-platform race tests, official OpenRouter SDK compatibility, release contracts, security, and reproducible release snapshots |
| CI-002 | DevOps | #72 | 2026-08-04 | Made Gosec SARIF artifacts available on pull-request and default-branch runs without paid Code Security, and removed unused elevated workflow permissions |
| CI-001 | DevOps | #71 | 2026-08-04 | Confirmed that default-branch CodeQL SARIF upload is unavailable because Code Security is disabled for the private repository |
| SVA16 | Architecture | #69 | 2026-08-04 | Established the Starport v1 concept seams, made Starmap v0.3.0 the provider and model fact owner, separated catalog-acquisition auth from inference auth, added OpenAI and OpenRouter protocol contracts, and passed the cross-platform CI, security, race, fuzz, and architecture gates |
| P1-S4-4.2b | Backend | #30 | 2025-01-10 | Valkey storage implementation with full KVStore interface, pub/sub support for cache invalidation, transaction support with MULTI/EXEC, atomic operations with Lua scripts, batch operations with auto-pipelining, integration tests, valkey-go client integration |
| P1-S4-4.2a | Backend | #29 | 2025-01-09 | Cache architecture refactoring with data-type-specific strategies, pub/sub invalidation for multi-node deployments, hybrid caching (local + distributed), automatic deployment mode detection, proper cache coherence for security-critical data (API keys, presets), distributed-only for rate limits, 75%+ test coverage |
| P1-S4-4.2 | Backend | #28 | 2025-01-08 | Caching system with Ristretto in-memory layer, KV store persistence, cache key generation, TTL management, cache policies, invalidation logic, cache warming, metrics tracking, proxy handler integration, 75.3% test coverage |
| P1-S4-4.1 | Security | #27 | 2025-01-08 | BYOK implementation with OpenRouter compatibility, 5% pricing model, AES-256-GCM encryption, Argon2id key derivation, fallback strategies, provider validation, BYOK manager, API endpoints, usage tracking, response headers, 75%+ test coverage |
| P1-S3-3.7 | Backend | #19 | 2025-01-08 | Dynamic model fetching for Anthropic/Gemini/Groq, split GeminiConnector into GoogleAIStudioConnector and VertexAIConnector, 1-hour cache TTL, Vertex AI models (PaLM, Codey, Claude), 85%+ test coverage |
| P1-S3-3.6 | Backend | #18 | 2025-01-08 | Provider metadata & /api/v1/providers endpoint, enhanced /api/v1/models with full metadata (pricing, context, architecture), /api/v1/models/{model}/endpoints, 85%+ test coverage |
| P1-S3-3.5 | Backend | #17 | 2025-01-08 | Provider routing with preferences (order/only/ignore), health tracking, latency-based routing, cost optimization, sticky sessions, 76.2% test coverage |
| P1-S3-3.4 | Backend | #16 | 2025-01-08 | OpenRouter-compatible model routing with fallback chains, auto model selection, provider preferences, bounded availability, model_used field in responses |
| P1-S3-3.3 | Backend | #15 | 2025-01-08 | Proxy endpoints implemented with /v1 and /api/v1 routes, streaming support, request validation, connector initialization from config, 85.4% test coverage |
| P1-S3-3.2 | Backend | #13 | 2025-01-08 | All 6 LLM provider connectors implemented with streaming, OpenRouter-compatible model IDs, 84.0% test coverage |
| P1-S3-3.1 | Backend | #12 | 2025-01-08 | Model Connector Interface with streaming support, health checks, mock implementation, 90.6% test coverage |
| P1-S2-2.3 | Storage | #10 | 2025-01-08 | Core storage models with APIKey, Preset, ProviderKey, TokenBucket, AES-256-GCM encryption, 91.9% test coverage |
| P1-S2-2.2 | Storage | #7 | 2025-01-08 | Badger DB integration with full KVStore implementation, TTL support, backup/restore, compaction, 100% test coverage |
| P1-S2-2.1 | Storage | #6 | 2025-01-08 | Storage interface with KVStore abstraction, error types, serialization, mock implementation, 82.4% test coverage |
| P1-S1-1.5 | API | #5 | 2025-01-07 | Configuration system with env vars, .env files, validation, and hot reload for rate limits |
| P1-S1-1.4 | API | #4 | 2025-01-07 | HTTP server with chi router, middleware, health checks, 93% test coverage |
| P1-S1-1.3 | DevOps | #3 | 2025-01-07 | Development environment with CI/CD, Docker, and pre-commit hooks |
| P1-S1-1.2 | Backend | #2 | 2025-01-07 | Project structure with CLI framework and clean architecture |
| P1-S1-1.1 | Foundation | #1 | 2025-01-07 | Repository initialized with go.mod, LICENSE, and badges |

### Known Issues

| Issue | Severity | Description | Workaround |
|-------|----------|-------------|------------|
| IDENTITY-001 | Low | A corrupt or foreign identity hash-index record can block operator deletion; runtime authentication fails closed | Repair the hash-index record before deletion |

## Current v1 Status

- Authentication uses hash-based identity lookup and fails closed.
- Response caching uses tenant-safe canonical semantic keys.
- HTTP middleware enforces rate limits through the concept repository.
- Starmap v0.3.0 owns provider, model, offering, endpoint, operation, and acquisition-auth facts.
- Starport owns inference credentials, request policy, route planning, execution, and protocol adaptation.
- OpenAI and OpenRouter raw protocol smoke tests pass.
- The pinned official OpenRouter Python, TypeScript, and Go SDK gates pass.
- Release archives, SBOMs, checksums, attestations, and GHCR publication are
  fail-closed release requirements.
- Independent checks verify public immutable release `v1.0.0` and its public
  GHCR image.

This file keeps task status and the Phase 1 completion history. The canonical
v1 design is in `docs/ARCHITECTURE.md`.
