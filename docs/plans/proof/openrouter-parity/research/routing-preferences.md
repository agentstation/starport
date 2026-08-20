# Explorer report: routing preferences (codex/console-revamp)

Headline: OpenRouter `provider` object parsed almost completely at the wire, then narrowed to 4 fields one function later. sort, max_price, data_collection, zdr, require_parameters accepted and silently discarded. quantizations = hard 400 (strict decode). Model variants (:free/:nitro/:floor) don't exist. No account-wide or server-wide default preference config.

## 1. Wire schema
- openrouter/codec.go:63-73 ProviderPreferences{Order, Only, Ignore, AllowFallbacks *bool, RequireParameters *bool, DataCollection string, ZDR *bool, Sort string, MaxPrice json.RawMessage}. On DecodedChat with Route string (:56-60).
- DROP POINT chat.go:71-82: copies only Order/Only/Ignore/AllowFallback (defaults true when absent). Sort/RequireParameters/DataCollection/ZDR/MaxPrice never read anywhere else.
- quantizations absent from struct + decodeStrict DisallowUnknownFields (codec.go:513-525) → 400. Verified empirically.
- OpenAI path (/v1): NO routing surface — no models/provider fields, strict decoder (openai/codec.go:18-43, :408). provider and models are 400s on /v1; no fallback chains there.
- inference.ChatRequest has no provider policy; policy travels beside via proxy.ChatCompletionRequest{Request, Route, Provider} (proxy/service.go:39-49).
- route validated to ""|"fallback" only (proxy/validator.go:60-62); also a cache-key input.
- Degradation chain: wire 9 fields → proxy.ProviderPreferences 4 (service.go:191-196, AllowFallback singular) → router.ProviderPreferences 4 (model_router.go:60-72) → routing.ProviderPolicy 4 (routing/types.go:93-98). Transforms at proxy/proxy.go:138-149 and router/planner_adapter.go:168-175.

## 2. Planner (internal/routing)
- Stateless Planner.Plan(Request, Snapshot) (planner.go:11-16). Request (types.go:107-120): Models, Operation, AllowModelFallbacks, AllowAnyModelFallback, RequiredCapabilities, RequiredContextTokens, EstimatedInput/OutputTokens, Tenant TenantPolicy{AllowedModels, AllowedProviders, ModelOverrides}, Providers ProviderPolicy, AffinityProvider, Optimization{PreferLowestCost, PreferLowestLatency}.
- Snapshot binds candidates to one CatalogGenerationID + AvailabilityRevision; validateSnapshot (planner.go:136-177) rejects cross-generation.
- rejectCandidate (planner.go:218-271) order: unavailable, unhealthy, missing operation, missing endpoint, tenant model, tenant provider, not-in-only, in-ignore, outside-order-when-no-fallbacks, missing capability, insufficient context. Stable RejectionCodes, Plan.Rejections().
- sortRankedCandidates (planner.go:314-345) lexicographic: ModelRank → ProviderRank (only if order supplied) → AffinityMatched → EstimatedCost asc (iff PreferLowestCost) → EstimatedLatency asc (iff PreferLowestLatency) → Route.ID(). Cost = InputPerToken*EstIn + OutputPerToken*EstOut (:347-353). Latency = measured EMA (LatencyTracker), known-before-unknown.
- CRITICAL: Optimization set from SERVER CONFIG only — planner_adapter.go:146-149: PreferLowestCost: r.config.EnableCostOptimization (default true, router.go:126, no Option to set), PreferLowestLatency: hardcoded true. Effective global policy: cost-then-latency for every request.
- TWO planning paths: catalog path (planner_adapter.go:138-184) vs registry fallback planRegistryRoute (planner_adapter.go:75-123) using filterByProviderPreferences (router/router.go:311-380) — independent order/only/ignore implementations; legacy quirk: `only` set → returns immediately, never consults `ignore` (router.go:320-333). Registry plan has generation ID "registry-runtime", EMPTY SelectionEvidence.
- AutoModelID "openrouter/auto" (router.go:23) → splitAutoModel (planner_adapter.go:57-73) sets AllowAnyModelFallback.
- Embeddings ignore provider preferences entirely (model_router.go:130-135; embeddings.go:114-130 never sets Providers).

## 3. Variants
- :free/:nitro/:floor NOT implemented anywhere. Variant-suffixed model decodes fine, reaches planner as literal string, matches nothing (exact-equality consideredModelRank planner.go:208-216) → ErrNoCandidate with ZERO rejections recorded (confusing failure shape).

## 4. Defaults / per-key
- No routing config keys in internal/config at all. No account/server default preferences. Presets never applied to requests.
- identity.APIKey persists AllowedModels only — NO AllowedProviders, NO ModelOverrides columns. getAPIKeyRoutingConfig (base.go:92-108) populates only AllowedModels + CredentialStrategy. AllowedProviders/ModelOverrides/RateLimitTier dead fields plumbed into TenantPolicy (planner_adapter.go:176-182) and cache key (proxy/cache.go:553-561). ModelOverrides has real planner logic (requestedModelRanks planner.go:185-206; tenantModelSet :388-405) unreachable by production code.
- Console has no routing UI.

## 5. Execution
- One total budget across plan: DefaultConfig (execution/types.go:129-138): MaxAttempts 3, MaxRetriesPerRoute 0, MaxElapsed 2m, backoff 100ms×2 max 2s. Retry and fallback share MaxAttempts.
- begin (executor.go:199-257): checks cancel → elapsed → attempts → routes; availability.Acquire refusal = StateSkipped, advances route WITHOUT consuming attempt.
- fail (executor.go:275-330): AttemptAction ContinueRoute/FallbackRoute/Stop/Default; Default: record failure → canRetry (Retryable) if routeRetry<MaxRetriesPerRoute else CanFallback → next route. Canceled → stop.
- succeed (executor.go:259-273): RecordSuccess only when credential.Owner != Tenant (BYOK stays out of operator circuit state).
- Evidence: AttemptEvidence with Transitions → router.Metadata.ModelsAttempted (model_router.go:147-175).

## Gap list vs OpenRouter
- 400 outright: provider.quantizations.
- Parsed then dropped: sort (BIGGEST — OptimizationPolicy already implements this ordering, wired only to server config), max_price (no price_exceeded RejectionCode), data_collection, zdr (no candidate data-policy facts), require_parameters (no param-set comparison).
- No wire field: provider.experimental.
- Supported end-to-end (on /api/v1 only): order, only, ignore, allow_fallbacks, models[] chains, openrouter/auto.
- Structural: no variant parsing; no per-request sort path; no defaults at any scope; AllowedProviders/ModelOverrides unreachable; embeddings no prefs; two divergent order/only/ignore impls; sort-vs-hardcoded-latency precedence decision needed.
