# Explorer report: presets, rate limits, budgets, tenancy (codex/console-revamp)

Headline: presets package fully built but completely unwired (not in binary dep graph); rate limiting is a single global fixed window keyed by API key ID; no spend/token budget concept anywhere.

## 1. Presets — package exists, completely unwired
- internal/presets: model.go, repository.go, repository_test.go only.
- Preset{ID, Name, Description, Config map[string]any, Version, CreatedAt, UpdatedAt}. Config is opaque — no typed inference fields, no model ref, no provider prefs. Validate() (model.go:25-42): non-empty ID, name ^[a-zA-Z0-9_-]+$ max 255, Version>=1, non-empty Config.
- Repository (repository.go:43-49): Create/Get/List/Update/Delete, CAS-versioned (Record{Revision uint64, Preset}), keyed by NAME: storageKey = "presets:v1:name:" + base64url(name) (repository.go:189-191). Name immutable (ErrNameImmutable :139-141).
- ZERO non-test importers; not in `go list -deps ./cmd/...`. Only refs: internal/architecture/import_graph_test.go:33,48,87.
- No preset reference path in requests: no preset field in openrouter codec (codec.go:19-52), zero "@preset" hits repo-wide.
- docs/ARCHITECTURE.md:33 lists preset REST endpoints as not implemented. internal/storage/README.md:95 documents STALE prefix `preset:{name}` contradicting live `presets:v1:name:`.
- Gap: Preset ID required by Validate() but storage keys on Name; nothing generates/looks up by ID.

## 2. Rate limits — one global fixed window, per-key subject
- internal/ratelimit: Decision{Allowed, Limit, Count, Remaining, ResetAt}; TokenBucket (model.go:16-21, RefillAt/TryConsumeAt :38-60) is DEAD CODE.
- Repository (repository.go:51-53): single method Consume(ctx, subject, limit, window) → Decision. Fixed window under ratelimit:v1:subject:+base64url(subject), CAS loop ≤64 attempts, TTL=window expiry (:82-144).
- Enforcement: internal/server/rate_limit.go:13-46. subject := "api_key:"+keyID (:28); limit/window from GLOBAL server config, never from key. Sets X-RateLimit-* + Retry-After; 429 rate_limit_error. Applied at routes.go:28 and :47 to all /v1 and /api/v1 after requireAPIKey.
- Requests only; no token limits, no per-tenant tier, no per-model, no concurrency.
- Config (server/config.go:35-37): ENABLE_RATE_LIMITING (default false), RATE_LIMIT_REQUESTS_PER_WINDOW (0), RATE_LIMIT_WINDOW (1m). Fed from app.go:748-750.
- INERT rich config: config/config.go:168-190 RateLimitingConfig (GlobalRequestsPerSecond, burst, per-hour, tokens per min/hour…) — only DefaultRequestsPerMinute + WindowSize read.
- INERT hot-reload rules: config/hot_reload.go:19-33 RateLimitRule/RateLimitRules per-key and per-model (requests+tokens, minute+hour, burst). app.go:376-394 constructs HotReloader but never calls OnUpdate; GetRateLimitRules/GetRuleForKey/GetRuleForModel have no non-test callers.
- No API surface reads/writes limits.

## 3. API keys
- identity.APIKey (model.go:12-23): ID, Name, Hash, Scopes, AllowedModels, RateLimitConfig map[string]any, Metadata, Active, CreatedAt, ExpiresAt. Helpers: IsExpiredAt, HasScope ("*" wildcard, :96-106), CanUseModel (empty=unrestricted, :109-119).
- RateLimitConfig untyped, never read (only cloneMap at identity/repository.go:538). Unsettable, unenforced.
- AllowedModels IS enforced (controllers/base.go:92-107 → routing planner) but CANNOT be set via any endpoint.
- Repository (identity/repository.go:49-58): Create, CreateInitial, ReleaseInitial, GetByID, GetByHash, List, Update, Delete. Namespace identity:v1:. GetByHash via secondary hash index.
- Admin endpoints (routes.go:84-101, requireAdmin): GET/POST /api/v1/admin/keys, GET/PUT/DELETE /api/v1/admin/keys/{key_id}.
- Create accepts only {name, description, scopes, metadata} (admin.go:75-80); update {name, scopes, metadata, active} (:169-174). AllowedModels/RateLimitConfig/ExpiresAt unsettable via HTTP. identity.IssueRequest (issuer.go:28-33) carries ExpiresAt but CreateKey never populates it.
- ListKeys hardcoded limit=100 offset=0, TODO at admin.go:41, fake pagination block. SystemInfo/Metrics return hardcoded placeholders (admin.go:249-296, storage type hardcoded "badger").
- No spend/token/cost limit or usage counter on keys. proxy.ErrInsufficientQuota → 402 (base.go:175-176) wired but NOTHING returns it. inference.Usage{Input,Output,Total,Reasoning} per response, nothing accumulates/persists. No usage: namespace.
- BYOK ProviderKey (credentials/model.go:24-35): UsageCount int64 + LastUsed incremented at byok/provider_keys.go:422-423. RateLimit *RateLimitConfig{RequestsPerMinute, TokensPerMinute} (model.go:17-21) persisted, never enforced, settable only via AddGlobalKey/UpdateGlobalKey.
- Provider-key routes (routes.go:64-81, requireKeyOwnership): CRUD + validate; /usage/provider-keys + /usage/comparison are 501 stubs (provider_keys.go:351-372).

## 4. Tenancy — tenant IS the API key
- base.go:75-81 getTenantID = requestctx.GetAPIKeyID. Set at chat.go:48, embeddings.go:49. Flows into response cache identity (cache/identity.go:61,69 ErrTenantRequired) and BYOK scoping (byok.UserScope, global scope "*").
- requireKeyOwnership (middleware.go:168-191): authKeyID == URL key_id, strict self-only.
- Flat scopes: admin, chat:write, embeddings:write, models:read, provider_keys:read/write, keys:read/write, "*". No roles/RBAC/hierarchy.
- proxy.APIKeyRoutingConfig (service.go:199-205): {AllowedProviders, AllowedModels, ModelOverrides, RateLimitTier, CredentialStrategy} — only AllowedModels + CredentialStrategy ever populated (base.go:103-106). Rest plumbed into cache policy key (proxy/cache.go:554-561) but always empty.

## Console gaps
- api.js calls: models, providers, admin/info, admin/metrics, admin/providers[/refresh], admin/keys CRUD, provider-keys CRUD+validate, usage/provider-keys, health/ready, chat/completions. No presets page, no limits/budget UI. Key-create form (keys.js:142-163) submits only {name} + optional scopes:["admin"].

## Gap list (verbatim from explorer)
1. presets unwired: needs app.go wiring, PresetsController, routes, typed Config schema decision, request-side reference (body field or @preset/slug parsing) — none exist.
2. Preset ID vs Name keying mismatch.
3. Rate limiting requests-only global; APIKey.RateLimitConfig, RateLimitingConfig token fields, RateLimitRule, RateLimitTier all unused shapes.
4. Hot-reload rule system never connected (OnUpdate never registered).
5. ratelimit.TokenBucket dead code.
6. No budget concept; ErrInsufficientQuota/402 path never triggered.
7. Admin key API can't set AllowedModels/RateLimitConfig/ExpiresAt.
8. Fake pagination; hardcoded SystemInfo/Metrics placeholders.
9. Both usage endpoints 501.
10. ProviderKey.RateLimit stored, never enforced.
11. No tenancy above API key.
12. storage/README.md:95 stale preset prefix doc.
