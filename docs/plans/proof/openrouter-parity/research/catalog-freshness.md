# Explorer report: catalog snapshot lifecycle + freshness (codex/console-revamp)

Headline: every catalog generation ever committed is still on disk, in full, with field-level provenance. An N-1→N diff is computable today with no new persistence. But nothing computes/exposes one, no HTTP endpoint triggers a catalog refresh, and no endpoint serves snapshot metadata beyond a bare generation ID.

## 1. Snapshot identity
- RoutableSnapshot (internal/catalog/snapshot.go:61-69): catalog *catalogs.Catalog, generationID, payloadChecksum, generatedAt, catalogSequence, availabilityRevision, routes []Route. Accessors :90-:245.
- TWO version axes: catalog facts (generationID/checksum/generatedAt/sequence from Starmap) vs availabilityRevision (Starport-local monotonic, bumped on adapter/availability change, control_plane.go:270,:174). One generation can republish many times with different revisions.
- ControlPlane holds atomic.Pointer[RoutableSnapshot]; every mutator derives a whole new snapshot (deriveRoutableSnapshot :296-359: keeps providers with adapters, drops Unavailable/Retired offerings, intersects ops, deterministic sort).
- Only projection: []Route (snapshot.go:11-20). No models/providers/prices projection; read on demand via Definition()/Offering()/Definitions(). Pricing on offering: catalogs.ProviderOffering (provider_offering.go:129-141) Pricing *ModelPricing, Limits, Availability, Regions, Endpoints, Lifecycle, Service, Modes.
- Upstream starmap CatalogState{Catalog, GenerationID, PayloadChecksum, GeneratedAt, Sequence} (client.go:75-81) — NO manifest.

## 2. Refresh contract + retention
- Two exclusive runtimes at boot (app.go:535-556): RemoteURL→OpenRemoteRuntime (SSE), else OpenRuntime (local). Validation makes exclusive (config/validation.go:78-108).
- App consumes catalogRuntime{ControlPlane(); RefreshCandidate(ctx, timeout) (starmap.CatalogState, error)} (app/runtime.go:28-31). Runtime.Refresh/Sync/PublishObservations have zero external callers. Live path: RefreshCandidate → syncCatalog (app.go:603) → activateRuntimeState (:614) → ReplaceRuntime (:660).
- Generation store keys (generation_store.go:17-22): catalog_generation:v1:current, catalog_remote_generation:v1:current, catalog_generation:v1:generation:<id>. Accept (remote_runtime.go:243-311) advances accepted head only after full validated runtime build; checksum+GeneratedAt verified; refuses backward moves. Restart: accepted generation = Starmap PinnedBootstrap (:97-131).
- Refresh triggers: startup opt-in (RefreshOnStart default FALSE, app.go:200-208); local ticker (RefreshInterval default 0s = never, :683-696); remote SSE sampler 250ms (remote_runtime.go:351-373 + app.go:698-714); credential reconcile republishes runtime but does NOT re-acquire catalog. NO HTTP endpoint or CLI triggers catalog refresh — POST /api/v1/admin/providers/refresh is credentials only.
- Retention: Commit (generation_store.go:102-155) write-once CAS; NO Delete/TTL/pruning anywhere. Full catalogs.Generation{Manifest, Payload []byte} JSON in KV.
- Diff N-1→N computable today: ScanWithPrefix (storage/interface.go:74) enumerates; each record has full CatalogPayload{SchemaVersion, Providers, Authors, ProviderModels, AuthorModels, Provenance}; DecodeCatalogPayload exported (payload_decode.go:39); GeneratedAt+Checksum give order + change test. Caveats: IDs unordered (need manifest read per record), no time index, unbounded growth, deep copies memory-hungry. CatalogSemanticChecksum (payload.go:41-56) EXCLUDES provenance — right checksum for "facts changed"; Payload.Checksum churns on provenance alone.

## 3. Exposed today: almost nothing
- GET /api/v1/admin/providers → providerstate.Snapshot{Revision (provider-state store revision — NOT catalog sequence NOT availability revision), CatalogGenerationID, Providers} (state/store.go:140-145). Admin scope only.
- Console: models.js:56-62,:108-114 shows `snapshot <gen id> · rev <revision>`; overview.js:186-198 same two fields. Falls back "snapshot — (admin key required)".
- LIVE BUG: models.js:63-78 "refresh catalog" button calls refreshProviders() (credentials endpoint) and toasts "Catalog refresh triggered" — mislabeled. providers.js:34-46 uses same endpoint with accurate toast.
- Never exposed: payload_checksum, generated_at, catalog_sequence, availability_revision, model/provider counts. /api/v1/admin/info is stub (hardcoded version "1.0.0"). Scope asymmetry: snapshot metadata admin-only vs models models:read.

## 4. Starmap provenance/freshness — rich, entirely unused
- provenance.Entry (starmap pkg/provenance/tracking.go:21-35): Source, Field, Value, Timestamp, ObservationID, ObservedAt, Revision, EvidenceChecksum, Rejections, Authority, Confidence, Reason, PreviousValue. Map keyed "resourceType:resourceID:fieldPath" (:180).
- Reachable NOW: snapshot.Catalog().Provenance() (readonly.go:232) → Map/Len/FindByField/FindByResource/FindModelField/FindModel/FormatYAML (interfaces.go:43-51). Starport references Provenance NOWHERE (zero grep hits).
- ModelPricing (model_pricing.go:66-82): EffectiveFrom/EffectiveUntil *utc.Time + IsEffectiveAt. With PreviousValue → price-delta surface well supported.
- Source IDs (evidence/source.go:17-33): providers, models_dev_git, models_dev_http, local_catalog, embedded_catalog, release_artifact.
- GenerationManifest (generation_manifest.go:176-190): ManifestVersion, SchemaVersion, GenerationID, GeneratedAt, Payload PayloadDescriptor{Checksum,SizeBytes,MediaType}, Validation{Status,ErrorCount,WarningCount,Checks}, SyncRunID, SourceObservations []SourceObservationLink{ObservedAt,Revision,Completeness,Status,EvidenceChecksum} (:146-154), ReviewCandidates, Completeness (complete|partial :54-63), Degraded bool, DegradationReasons, ConsumerCompatibility. Persisted with every generation, NEVER read by Starport. newRoutableSnapshot (snapshot.go:71-87) copies five scalars, discards manifest; reach it via GenerationStore.Get by ID.

## 5. Offline behavior: silent
- No offline/air-gap/degraded/staleness concept in Starport Go source.
- Starmap fallback ladder (client.go:200-262): embedded bootstrap → durable current overrides → local YAML only when store empty. Remote mode pins last accepted generation.
- Failure = single Warn log everywhere (app.go:206, :692, :459-461, :708-710). No metric/counter/state flag/readiness impact. Startup does NOT fail on acquisition failure (app_test.go:105-138). No Info log on success.
- /health/ready returns ok unconditionally (health.go:36-52, TODO). starport doctor prints gen ID + provider count, not timestamp/age (diagnosis/service.go:144-164).
- Starmap staleness APIs never wired: starmap.Readiness() → CatalogReadiness{Ready, Embedded, Issues: catalog_unavailable, embedded_bootstrap_future/stale/oversize} (readiness.go); WithEmbeddedBootstrapMaxAge/MaxSizeBytes (options.go:92-113, disabled by default); remote.Subscriber.Health() → StreamState, CatalogAgeSeconds, LastError, Retries, PollingFallback (remote/health.go:41-80); remote.Config.PollingFallback left nil (remote_runtime.go:34-40).
- CatalogConfig (config.go:41-52, STARPORT_CATALOG_): WorkspacePath, RefreshOnStart=false, RefreshInterval=0s, RefreshTimeout=2m, RemoteURL, RemoteAPIKey, RemoteActivationInterval=250ms. Dev mode force-disables both refresh paths (storage.go:37-38).

## Gap list for freshness/diff surface
Durable-but-never-read: entire GenerationManifest (degraded, reasons, completeness, per-source observations, validation, payload size); all provenance (PreviousValue, Rejections, Authority, Confidence, Source); every historical generation payload.
- What changed on refresh: enumerate N-1/N (ScanWithPrefix), decode both (DecodeCatalogPayload), compare; use CatalogSemanticChecksum to skip provenance-only churn.
- Models added/removed: diff ProviderModels/AuthorModels keys, or diff snapshot.Routes() for ROUTABLE delta (different question — decide which console shows).
- Price deltas: ProviderOffering.Pricing per OfferingKey across payloads + provenance PreviousValue/Source/ObservedAt + EffectiveFrom/Until.
- Per-provider coverage: manifest SourceObservations per-source Completeness/Status; routable coverage = RoutesForProvider() vs Catalog().ProviderOfferings().

Plumbing gaps in dependency order:
1. RoutableSnapshot discards manifest; expose degraded/completeness via GenerationStore.Get at activation or thread through newRoutableSnapshot.
2. No snapshot-metadata endpoint; natural shape = catalog-owned controller beside provider_operations.go.
3. No genuine catalog-refresh endpoint; models.js promises one and calls credentials instead — fix label or add route.
4. Scope mismatch: freshness bar for non-admins needs lower-scoped read route.
5. No generation index/pruning; listing = full scan + full payload decode per record. Add small ordered index record + retention policy with the diff feature.
6. Three counters (Sequence, availabilityRevision, provider-state Revision); console shows the third labelled as snapshot's. Name distinctly.
7. No staleness threshold/max age/readiness contribution/metric. Ready-made hooks: WithEmbeddedBootstrapMaxAge, Subscriber.Health().
8. No metrics infrastructure in repo at all.
