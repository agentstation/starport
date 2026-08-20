# Explorer report: per-request usage/accounting (codex/console-revamp)

Bottom line: Starport records nothing per request. No usage table, request log, counters, or metrics backend. Token counts live in memory only until encoded into the HTTP response, then discarded. All raw material exists (usage normalization, provider+model identity at completion, full Starmap pricing, KV repository convention, unused proxy middleware seam). Nothing joins them.

## 1. Per-request data and where it dies
- HTTP access log (server/logger.go:12, routes.go:125): method, path, remote_addr, client_ip, request_id (middleware.GetReqID), status, bytes (post-compression), duration. NOT logged: key ID/tenant, model, provider, tokens, cost. Always Info level.
- execution.AttemptEvidence (execution/types.go:72-82): Number, Route, Retry, State, StartedAt/FinishedAt, Duration, Failure, Transitions. On ChatResult (:157-164).
- router.Metadata (model_router.go:147-157): ModelsAttempted []ModelAttempt{Model, Provider, Error, Duration, Status}, RoutingDuration, SelectionReason (built execution_adapter.go:223-245). router.Response (:107-127): ModelUsed, ProviderUsed, Attempts, *Metadata, CatalogSnapshot (populated router.go:276-283).
- ALL DISCARDED at proxy.go:303-322. proxy.ChatCompletionResponse (service.go:143-152) keeps only Response, CacheStatus, CacheAge, ETag, CacheCost. Provider recoverable only by splitting ModelUsed ("providerID/providerModelID").
- Streaming worse: ManagedStream exposes Attempts()/Committed()/ModelUsed() but proxy stream response = Read()/Close() only (service.go:63-70).
- Cache status available at controller: CacheStatus/CacheAge + CacheStatusProvider (service.go:215-218) → X-Cache header.
- KEY SEAM UNUSED: proxy.Middleware/Chain (proxy/middleware.go:12-35), LoggingMiddleware (:44), TimingMiddleware (:248) + GetRequestStartTime (:270); proxy.WithMiddleware (proxy.go:59, doc :81 shows metricsMiddleware example). app.go:351-359 passes only WithCache. Zero call sites.
- Also: execution.OutcomePublisher (types.go:110-112) non-blocking publisher port — AttemptOutcome (:104-108) has route/credential/failure, NO usage. availability.Publisher = precedent for derived-projection port.
- Only cost arithmetic: proxy.go:312-323 cache-write cost when client sent cache_control; cacheTokenPrices :700-718; bills ALL prompt tokens as writes, readCost hardcoded 0. → X-Cache-*-Cost headers (chat.go:124-130). No streaming counterpart.
- Response cache persists inference.ChatResponse (incl. Usage) keyed by semantic hash — not enumerable by tenant/time. Streaming usage latched last-writer-wins at response/cache/stream.go:55-57. ChatResponse has no json tags (serializes {"InputTokens":…}).

## 2. Storage layer
- Backends: Badger (default), Valkey/Redis, MockStore, readOnlyStore. NO SQL/sqlite anywhere.
- storage.KVStore (interface.go:45-79): Get/Set/Delete/Exists, SetWithTTL/GetTTL/ExpireAt, Increment/Decrement, CompareAndSwap(+Batch), BatchGet/Set/Delete/SetWithTTL, BeginTransaction, Scan, ScanWithPrefix, Ping, Close.
- CRITICAL for time-ordered log: ScanWithPrefix returns KEYS ONLY (N+1 Get loop), ordering NOT guaranteed cross-backend (Badger lexicographic badger.go:683-720; Valkey SCAN unordered valkey.go:422-424; Mock random map). Repos sort.Strings after. NO cursor/pagination/start-key/range/reverse. limit truncates BEFORE caller sort → nondeterministic subsets on Valkey/Mock. limit<=0 = unlimited.
- ⇒ time-ordered listing needs time in key (zero-padded, e.g. usage:v1:tenant:<b64>:ts:<20-digit-unix-nanos>:<id>) + caller-side sort. Retention via SetWithTTL.
- CompareAndSwapBatch = real atomicity (Badger one db.Update, badger.go:431-501). No repo uses BeginTransaction; ValkeyTransaction.CompareAndSwap ignores `old` (valkey.go:511) — not portable.
- Existing repos: identity (identity:v1:key:/hash:/initial/collection), credentials (credentials:v1:scope:<b64>:provider:<b64> — CLOSEST ANALOGUE for tenant-scoped listing), presets (presets:v1:name:), ratelimit (ratelimit:v1:subject:, only TTL user), catalog GenerationStore, response/cache (own narrow Store port, "response:" prefix via cache/manager.go:81).
- storage/keys.go NOT a registry (only KeyPrefixResponse + unused KeyPrefixModel); storage/README.md:90-98 key docs all stale.
- Repository convention (presets = minimal template, credentials = scoped-key template): one repository.go + repository_test.go + model.go; interface in owning concept package importing internal/storage; import_graph_test.go:83-91 enforces exactly-one-internal-import for identity/credentials/ratelimit/presets — NEW REPO PACKAGE MUST BE ADDED THERE; consts StorageSchemaVersion=1, StoragePrefix, defaultListLimit=1000; sentinels ErrRepositoryRequired/ErrNotFound/ErrConflict/ErrCorruptRecord; Record{Revision uint64, Domain}; unexported repository{store}; wire record with schema_version/revision json tags; Open(store) (+Clock param when time involved, nil→systemClock); direct encoding/json; keys <concept>:v1:<part>:<base64.RawURLEncoding(id)> with list-dimension FIRST; CAS revisions (nil expected = must-not-exist, nil new = delete); mapReadError/mapConflict; TTL==0 in CompareAndSwapMutation PRESERVES existing expiry (badger.go:489-491).
- Contract test: repotest.Run(t, contract) (backends.go:16) — memory, badger (TempDir), valkey (skip UNVERIFIED without TEST_VALKEY_URL). Tests internal package (assert unexported storageKey + raw JSON). testify/require + google/uuid. New KVStore method needs namespacing override in backends.go.

## 3. Usage parsing/normalization
- inference.Usage{InputTokens, OutputTokens, TotalTokens, ReasoningTokens} (types.go:162-167); on ChatResponse (:200), EmbeddingResponse (:266), StreamEvent.Usage *Usage (:238), cloned :314.
- Single normalization seam: usageToInference/usageFromInference (connectors/inference_adapter.go:445-464) via OpenAI-shaped connectors.Usage{PromptTokens, CompletionTokens, TotalTokens, *CompletionTokensDetails{ReasoningTokens}} (types.go:197-208).
- Per-connector: OpenAI has reasoning, DROPS cache tokens (no prompt_tokens_details field). Anthropic computes total, no reasoning, DROPS cache_creation/cache_read (no struct fields, anthropic.go:346-349). Google has reasoning (thoughtsTokenCount), CachedContentTokenCount decoded (gemini_types.go:27) but NEVER read. Ollama computes total. Vertex embeddings FABRICATE word count as tokens + unchecked req.Input.(string) assertion panics on []string (vertex_ai.go:138-141). AIStudio embeddings all zeros.
- ACCURACY DEFECTS: Anthropic streaming PromptTokens==0 (message_start not modeled — anthropic.go:476-478); Vertex embedding word counts.
- Streaming: StreamEventsToInference (inference_adapter.go:179-223) emits separate StreamEvent{Kind: StreamUsage} appended after delta (:211-221), unconditional — does NOT consult StreamOptions.IncludeUsage, unlike cache replay (response/cache/stream.go:37) ⇒ cache HIT vs MISS produce different SSE frame sequences.
- execution never touches usage (pure pass-through); ChatController.handleStream never reads event.Usage. After stream, usage reaches HTTP client but NOT gateway (except caching wrapper proxy/cache.go:654-682).
- Non-streaming: usage converted THREE times (connector→inference→execution→inference→transform→encode via openai/codec.go:274 / openrouter/codec.go:333 encodeUsage).
- Usage consumed outside encoding: exactly 2 places (cache-cost block, response cache). No metrics/OTel/Prometheus in internal/. No token count in any log. ratelimit counts requests not tokens.

## 4. Prices at completion time — YES, fully reachable
- RoutableSnapshot retains full *catalogs.Catalog; Offering(route) → ProviderOffering.Pricing (snapshot.go:246-251). ControlPlane.Current() O(1).
- starmap ModelPricing (model_pricing.go:66-82): Tokens *ModelTokenPricing{Input, Output, Reasoning, CacheRead, CacheWrite *ModelTokenCost{PerToken, Per1M}}, Operations{Request, ImageInput, AudioInput, VideoInput, ImageGen, AudioGen, VideoGen, WebSearch, FunctionCall, ToolUse *float64}, Tiers, Currency, EffectiveFrom/Until, IsEffectiveAt (never called by Starport).
- Pricing per PROVIDER-OFFERING, not model definition.
- Provider+model at completion: router.Response{ModelUsed, ProviderUsed, CatalogSnapshot}. Runtime lease in context (connectors.RuntimeLeaseFromContext, runtime.go:44-59). Streaming weaker: ManagedStream.ModelUsed() string only — no route/snapshot.
- Lossy intermediate: routing.TokenCost{InputPerToken, OutputPerToken} only; EstimatedCost = len/4 heuristic pre-flight (proxy.go:195-208), never reconciled.
- Wire: /v1/models strips pricing (models.go:154-161); /api/v1/models emits only {prompt, completion} — OpenRouter's request/image/web_search/internal_reasoning/input_cache_read/input_cache_write fields DON'T EXIST as struct fields (openrouter/codec.go:465-469). Currency dropped. Edge: Pricing!=nil but Tokens.Input==nil → Prompt "" not "0" (proxy.go:515-524); pricing from routes[0] = alphabetically-first provider (proxy.go:498-522).

## 5. HTTP layout
- chi v5. Global middleware order (routes.go:121-133): RequestID, ClientIP, Logging, Recoverer, SecurityHeaders, SizeLimiter, Timeout, CORS, Compress(5).
- Usage-shaped existing: two 501 stubs GET /api/v1/keys/{key_id}/usage/provider-keys + /usage/comparison (provider_keys.go:351-372, requireKeyOwnership, provider_keys:read OR keys:read); GET /api/v1/admin/metrics = hardcoded all-zeros literal (admin.go:274-295, TODO :275) — console overview ALWAYS shows 0 requests/0 errors/0ms (overview.js:154-168); admin/info stub; api.js providerKeyUsage exported, imported by no page.
- Absent: /api/v1/generation, /credits, /activity, /key, analytics/spend/stats routes; internal/usage|billing|metering|telemetry packages.
- Controller convention: BaseHandler{service, protocol} (base.go:41-57); one controller struct instantiated per dialect (NewX/NewOpenRouterX/newX(service, protocol)). Aggregate: controllers.Controllers + NewControllers(Config{Service, ProviderKeys, Identities, ProviderOperations, ServiceName, Version, Console}) (controllers.go:38, :28-34). New repo dep threads server.Dependencies (server.go:66-84) → controllers.Config → NewControllers, wired app.go:396-400.
- BaseHandler.getRequestID BROKEN (base.go:61 reads ctx.Value("request_id") untyped; chi stores under unexported type → always ""). logger.go:28 does it right (middleware.GetReqID).
- Non-BaseHandler controllers (ProviderKeys, Admin, Health, ProviderOperations) use dto.WriteJSON, protocol-agnostic. dto has NO list envelope/pagination helper (responses.go 75 lines).
- requestctx: WithAPIKey/WithAPIKeyID/WithAPIKeyModel + getters only. Request ID NOT in requestctx. getTenantID aliases GetAPIKeyID. proxy.ChatCompletionRequest already carries TenantID + RequestID (service.go:139-141).
- Console: routes all unauthenticated GET at root (handler.go:205-210); key in localStorage starport.apiKey; no usage view; only per-turn tokens in chat playground (chat.js:408-412), never aggregated.

## Gap list
Recording: no per-request record/package/metrics backend; no token counts in logs; admin/metrics zero literal rendered as truth by console; byok.Usage + RecordUsage exist but impl discards param (`_ *Usage`, provider_keys.go:418-431) and has no non-test caller.
Thrown away: router.Metadata + CatalogSnapshot dropped at proxy.go:303-322; streaming exposes neither usage nor route to gateway; provider identity only as ModelUsed substring; cache-read/write tokens dropped at every connector; Anthropic streaming zero prompt tokens.
Storage: no SQL/time-series/append-log; ScanWithPrefix keys-only no order/cursor; no tenant/time-dimension repo; response cache not enumerable.
Cost: none computed except flawed cache_control estimate; no cost field on any Usage type; IsEffectiveAt never called; Starmap's rich pricing never projected.
HTTP: no generation/credits/activity/key routes; 501 usage stubs; no pagination convention; no tenant above key; getRequestID broken.
Unused seams ready: proxy.Middleware/WithMiddleware (THE insertion point — model+provider+usage+duration+cache status coexist there); execution.OutcomePublisher; availability.Publisher pattern.
