package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogmeta"
	"github.com/agentstation/starmap/pkg/catalogremote"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/pkg/catalogstore"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/providerauth"
	"github.com/agentstation/starport/internal/providers"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/registry"
	"github.com/agentstation/starport/internal/storage"
)

func TestVerifiedRemoteCatalogActivatesProvider(t *testing.T) {
	inferenceServer := newSyntheticOpenAIServer(t)
	defer inferenceServer.Close()
	generation := remoteAppTestGeneration(
		t,
		"remote-acme",
		syntheticInferenceCatalog(t, inferenceServer.URL),
		time.Now().UTC(),
	)
	const remoteAPIKey = "remote-catalog-secret"
	catalogServer := newRemoteCatalogServer(t, generation, remoteAPIKey)
	defer catalogServer.Close()

	cfg, err := config.NewLoader().
		WithEnvironment(map[string]string{
			"OPENAI_API_KEY": "sk-openai-test",
			"ACME_API_KEY":   "acme-secret",
		}).
		Load(t.Context())
	require.NoError(t, err)
	store := storage.NewMockStore()
	remoteRuntime, err := runtimecatalog.OpenRemoteRuntime(
		t.Context(),
		store,
		runtimecatalog.RemoteConfig{
			BaseURL:            catalogServer.URL + "/api/v1",
			APIKey:             remoteAPIKey,
			ActivationInterval: time.Millisecond,
			HTTPClient:         catalogServer.Client(),
		},
	)
	require.NoError(t, err)
	plane := remoteRuntime.ControlPlane()
	resolved, err := cfg.ResolveProviderSet(
		t.Context(),
		plane.Current().Catalog().Providers(),
		config.ProvidersConfig{},
	)
	require.NoError(t, err)
	transports, err := connectors.ProductionTransportRegistry()
	require.NoError(t, err)
	authentication, err := providerauth.ProductionRegistry()
	require.NoError(t, err)
	newConnector := func(
		_ string,
		_ []catalogs.EndpointType,
		providerConfig connectors.ProviderConfig,
	) (connectors.Connector, error) {
		return connectors.NewMockConnector(providerConfig), nil
	}
	registrations, err := buildRegistrations(
		plane.Current().Catalog(),
		transports,
		authentication,
		providers.Configurations(resolved),
		newConnector,
	)
	require.NoError(t, err)
	runtimeRegistry, err := registry.Open(plane, registrations)
	require.NoError(t, err)
	application := &App{
		config: cfg, providerSettings: config.ProvidersConfig{},
		catalogRuntime: remoteRuntime, catalogUpdates: remoteRuntime,
		catalog: plane, registry: runtimeRegistry,
		transports: transports, authentication: authentication,
		newConnector: newConnector,
	}

	runCtx, cancel := context.WithCancel(t.Context())
	require.NoError(t, remoteRuntime.Start(runCtx))
	require.NoError(t, application.activateRuntimeState(
		t.Context(),
		remoteRuntime.CurrentCandidate(),
	))
	require.Equal(t, generation.Manifest.GenerationID, plane.Current().GenerationID())
	require.Equal(
		t,
		generation.Manifest.Payload.Checksum,
		plane.Current().PayloadChecksum(),
	)
	_, err = runtimeRegistry.Get("acme")
	require.NoError(t, err)
	routes := plane.Current().RoutesForProvider("acme")
	require.NotEmpty(t, routes)
	var foundOpaqueChat bool
	for _, route := range routes {
		if route.ProviderModelID == "opaque/chat@001" {
			foundOpaqueChat = true
			break
		}
	}
	require.True(t, foundOpaqueChat)

	acceptedStore, err := runtimecatalog.NewGenerationStore(store)
	require.NoError(t, err)
	accepted, err := acceptedStore.Current(t.Context())
	require.NoError(t, err)
	require.Equal(t, generation.Manifest.GenerationID, accepted.Manifest.GenerationID)

	cancel()
	require.NoError(t, remoteRuntime.Close(t.Context()))
	require.NoError(t, runtimeRegistry.Close())

	restarted, err := runtimecatalog.OpenRemoteRuntime(
		t.Context(),
		store,
		runtimecatalog.RemoteConfig{
			BaseURL:            catalogServer.URL + "/api/v1",
			ActivationInterval: time.Millisecond,
			HTTPClient:         catalogServer.Client(),
		},
	)
	require.NoError(t, err)
	require.Equal(
		t,
		generation.Manifest.GenerationID,
		restarted.ControlPlane().Current().GenerationID(),
	)
	_, err = restarted.ControlPlane().Current().Catalog().Provider("acme")
	require.NoError(t, err)
	require.NoError(t, restarted.Close(t.Context()))
}

func TestRemoteCatalogCandidateFailureRetainsRuntimeAndCacheIdentity(t *testing.T) {
	fixture := newRuntimeRefreshFixture(t)
	before := fixture.registry.Snapshot()
	builder := catalogs.NewEmpty()
	invalidCatalog, err := builder.Build()
	require.NoError(t, err)
	updates := &recordingCatalogUpdates{}
	fixture.application.catalogUpdates = updates

	err = fixture.application.activateRuntimeState(t.Context(), starmap.CatalogState{
		Catalog: invalidCatalog, GenerationID: "remote-unroutable",
		PayloadChecksum: "remote-unroutable-checksum",
		GeneratedAt:     time.Now().UTC(),
	})
	require.Error(t, err)
	require.Same(t, before, fixture.registry.Snapshot())
	require.Equal(t, before.GenerationID(), fixture.registry.Snapshot().GenerationID())
	require.Equal(t, int32(0), updates.accepted.Load())
}

func TestRemoteCatalogDuplicateAndDigestEqualIdentity(t *testing.T) {
	fixture := newRuntimeRefreshFixture(t)
	before := fixture.registry.Snapshot()
	updates := &recordingCatalogUpdates{}
	fixture.application.catalogUpdates = updates
	duplicate := starmap.CatalogState{
		Catalog: before.Catalog(), GenerationID: before.GenerationID(),
		PayloadChecksum: before.PayloadChecksum(),
		GeneratedAt:     before.GeneratedAt(),
		Sequence:        before.CatalogSequence(),
	}
	require.NoError(t, fixture.application.activateRuntimeState(t.Context(), duplicate))
	require.Same(t, before, fixture.registry.Snapshot())
	require.Equal(t, int32(0), updates.accepted.Load())

	digestEqual := duplicate
	digestEqual.GenerationID = "digest-equal-new-identity"
	digestEqual.GeneratedAt = duplicate.GeneratedAt.Add(time.Minute)
	digestEqual.Sequence++
	require.NoError(t, fixture.application.activateRuntimeState(t.Context(), digestEqual))
	after := fixture.registry.Snapshot()
	require.Equal(t, digestEqual.GenerationID, after.GenerationID())
	require.Equal(t, duplicate.PayloadChecksum, after.PayloadChecksum())
	require.Same(t, duplicate.Catalog, after.Catalog())
	require.Equal(t, int32(1), updates.accepted.Load())
}

func TestRemoteCatalogAcceptanceFailureRetainsRuntime(t *testing.T) {
	fixture := newRuntimeRefreshFixture(t)
	before := fixture.registry.Snapshot()
	updates := &recordingCatalogUpdates{err: errors.New("durable store unavailable")}
	fixture.application.catalogUpdates = updates
	state := starmap.CatalogState{
		Catalog: before.Catalog(), GenerationID: "remote-not-durable",
		PayloadChecksum: before.PayloadChecksum(),
		GeneratedAt:     before.GeneratedAt().Add(time.Minute),
		Sequence:        before.CatalogSequence() + 1,
	}

	err := fixture.application.activateRuntimeState(t.Context(), state)
	require.ErrorContains(t, err, "durable store unavailable")
	require.Same(t, before, fixture.registry.Snapshot())
	require.Equal(t, int32(1), updates.accepted.Load())
	require.Equal(t, int32(1), fixture.newConnector.closed.Load())
}

type recordingCatalogUpdates struct {
	accepted atomic.Int32
	err      error
}

func (*recordingCatalogUpdates) Start(context.Context) error { return nil }
func (*recordingCatalogUpdates) CurrentCandidate() starmap.CatalogState {
	return starmap.CatalogState{}
}
func (*recordingCatalogUpdates) Updates() <-chan starmap.CatalogState { return nil }
func (updates *recordingCatalogUpdates) Accept(context.Context, starmap.CatalogState) error {
	updates.accepted.Add(1)
	return updates.err
}
func (*recordingCatalogUpdates) Close(context.Context) error { return nil }

func newRemoteCatalogServer(
	t testing.TB,
	generation catalogstore.Generation,
	apiKey string,
) *httptest.Server {
	t.Helper()
	manifest, err := catalogremote.MarshalManifest(generation.Manifest)
	require.NoError(t, err)
	return httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.Header.Get("X-API-Key") != apiKey {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		resourcePath := strings.TrimPrefix(request.URL.Path, "/api/v1")
		switch resourcePath {
		case catalogremote.ManifestPath,
			catalogremote.GenerationManifestPath(generation.Manifest.GenerationID):
			writer.Header().Set("Content-Type", catalogremote.ManifestMediaType)
			_, _ = writer.Write(manifest)
		case catalogremote.PayloadPath(generation.Manifest.GenerationID):
			writer.Header().Set("Content-Type", catalogs.CatalogPayloadMediaType)
			_, _ = writer.Write(generation.Payload)
		case catalogremote.EventStreamPath:
			writer.Header().Set("Content-Type", "text/event-stream")
			writer.WriteHeader(http.StatusOK)
			writer.(http.Flusher).Flush()
			<-request.Context().Done()
		default:
			http.NotFound(writer, request)
		}
	}))
}

func remoteAppTestGeneration(
	t testing.TB,
	generationID string,
	catalog *catalogs.Catalog,
	generatedAt time.Time,
) catalogstore.Generation {
	t.Helper()
	payload, err := catalogstore.EncodeCatalogPayload(catalog)
	require.NoError(t, err)
	descriptor := catalogs.DescribeCatalogPayload(payload)
	generation := catalogstore.Generation{
		Manifest: catalogs.GenerationManifest{
			ManifestVersion: catalogs.CurrentGenerationManifestVersion,
			SchemaVersion:   catalogs.CurrentCatalogSchemaVersion,
			GenerationID:    generationID,
			GeneratedAt:     generatedAt,
			Payload:         descriptor,
			Validation: catalogs.GenerationValidationReport{
				ValidatorVersion: "starport-remote-app-test/v1",
				ValidatedAt:      generatedAt,
				Status:           catalogs.GenerationValidationPassed,
				Checks: []catalogs.GenerationValidationCheck{{
					Name: "test", Status: catalogs.GenerationValidationCheckPassed,
				}},
			},
			SyncRunID: "sync-" + generationID,
			SourceObservations: []catalogs.SourceObservationLink{{
				Source:        catalogmeta.LocalCatalogID,
				ObservationID: "observation-" + generationID,
				ObservedAt:    generatedAt,
				Revision: catalogmeta.ObservationRevision{
					Kind:  catalogmeta.ObservationRevisionKindContentDigest,
					Value: descriptor.Checksum,
				},
				Completeness:     catalogmeta.ObservationCompletenessComplete,
				Status:           catalogmeta.ObservationStatusSucceeded,
				EvidenceChecksum: descriptor.Checksum,
			}},
			ReviewCandidates: []catalogmeta.ReviewCandidate{},
			Completeness:     catalogs.GenerationCompletenessComplete,
			ConsumerCompatibility: catalogs.ConsumerCompatibility{
				MinSchemaVersion: catalogs.CurrentCatalogSchemaVersion,
				MaxSchemaVersion: catalogs.CurrentCatalogSchemaVersion,
			},
		},
		Payload: payload,
	}
	require.NoError(t, generation.Validate())
	return generation
}
