package catalog

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starmap/pkg/sources"
	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestProductionAcquisitionLifecycle(t *testing.T) {
	var providerCalls, metadataCalls atomic.Int64
	var version atomic.Int64
	version.Store(1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		if r.URL.Path != "/models" || r.Header.Get("Authorization") != "Bearer catalog-fixture-key" {
			t.Error("provider request did not use the configured acquisition credential")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":[{"id":"fixture-chat","name":"Provider observation %d","context_window":12345}]}`, version.Load())
	}))
	t.Cleanup(server.Close)
	metadata := bytes.ReplaceAll(metadataSourcePayload(t), []byte("gpt-image-2"), []byte("fixture-chat"))
	var metadataProviders map[string]map[string]any
	require.NoError(t, json.Unmarshal(metadata, &metadataProviders))
	models := metadataProviders["openai"]["models"].(map[string]any)
	facts := models["fixture-chat"].(map[string]any)
	facts["modalities"] = map[string]any{"input": []string{"text"}, "output": []string{"text"}}
	facts["cost"] = map[string]any{"input": 1, "output": 2}
	metadata, err := json.Marshal(metadataProviders)
	require.NoError(t, err)
	prior := http.DefaultTransport
	http.DefaultTransport = metadataTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://models.dev/api.json" {
			return nil, fmt.Errorf("unexpected metadata request: %s", r.URL.Host)
		}
		metadataCalls.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(metadata)), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = prior })
	settings := acquisitionCatalogSettings(t, server.URL)
	settings.SourceCacheDirectory = filepath.Join(t.TempDir(), "cache")
	settings.AcquisitionEnabled = true
	settings.AcquisitionInterval = time.Hour
	settings.Values = map[string]string{catalogconfig.AcquisitionSources: string(sources.ProvidersID) + "," + string(sources.ModelsDevHTTPID), catalogconfig.StartupSpread: "0s", catalogconfig.CoalesceWindow: "1ms"}
	storePath := filepath.Join(t.TempDir(), "badger")
	openStore := func() *storage.BadgerStore {
		store, err := storage.OpenBadger(storage.BadgerConfig{Path: storePath, SyncWrites: true, NumVersions: 1, NumLevelZero: 5, MemTableSize: 64 << 20})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, store.Close()) })
		return store
	}
	lookup := func(name string) (string, bool) {
		if name == "STARPORT_OPENAI_API_KEY" {
			return "catalog-fixture-key", true
		}
		return "", false
	}
	store := openStore()
	connected, err := OpenRuntime(t.Context(), store, settings, lookup)
	require.NoError(t, err)
	initialRuntime := connected
	t.Cleanup(func() { require.NoError(t, initialRuntime.Close(context.Background())) })
	require.Eventually(t, func() bool {
		candidate, err := connected.CurrentCandidate(t.Context())
		if err != nil {
			return false
		}
		provider, err := candidate.State.Catalog.Provider("openai")
		if err != nil {
			return false
		}
		model := provider.Models["fixture-chat"]
		return model != nil && model.Name == "Provider observation 1" && model.Limits != nil && model.Limits.OutputTokens == 4096
	}, time.Minute, 20*time.Millisecond, "automatic startup must reconcile both collectors")
	first, err := connected.CurrentCandidate(t.Context())
	require.NoError(t, err)
	require.NoError(t, connected.Accept(t.Context(), first))
	require.NoError(t, connected.ControlPlane().Activate(first.State))
	checkProjection := func() {
		candidate, err := connected.CurrentCandidate(t.Context())
		require.NoError(t, err)
		observed, err := candidate.State.Catalog.Offering("openai", "fixture-chat")
		require.NoError(t, err)
		require.NoError(t, connected.ControlPlane().ReplaceAdapters([]AdapterAvailability{testAdapterAvailability("openai", observed, true)}))
		snapshot := connected.ControlPlane().Current()
		routes := snapshot.RoutesForProvider("openai")
		require.NotEmpty(t, routes)
		var selected Route
		for _, route := range routes {
			if route.ProviderModelID == "fixture-chat" {
				selected = route
				break
			}
		}
		require.EqualValues(t, "fixture-chat", selected.ProviderModelID)
		offering, err := snapshot.Offering(selected)
		require.NoError(t, err)
		require.NotNil(t, offering.Limits)
		require.EqualValues(t, 12345, offering.Limits.ContextWindow)
		require.EqualValues(t, 4096, offering.Limits.OutputTokens)
	}
	checkProjection()
	version.Store(2)
	_, err = connected.Refresh(t.Context())
	require.NoError(t, err)
	second, err := connected.CurrentCandidate(t.Context())
	require.NoError(t, err)
	require.NotEqual(t, first.State.GenerationID, second.State.GenerationID)
	provider, err := second.State.Catalog.Provider("openai")
	require.NoError(t, err)
	require.Equal(t, "Provider observation 2", provider.Models["fixture-chat"].Name)
	require.NoError(t, connected.Accept(t.Context(), second))
	require.NoError(t, connected.ControlPlane().Activate(second.State))
	checkProjection()
	accepted, err := connected.accepted.Get(t.Context(), second.State.GenerationID)
	require.NoError(t, err)
	observed := map[sources.ID]bool{}
	for _, observation := range accepted.Manifest.SourceObservations {
		observed[observation.Source] = true
	}
	require.True(t, observed[sources.ProvidersID])
	require.True(t, observed[sources.ModelsDevHTTPID])
	require.NoError(t, connected.Close(t.Context()))
	require.NoError(t, store.Close())
	providerBefore, metadataBefore := providerCalls.Load(), metadataCalls.Load()
	require.Positive(t, providerBefore)
	require.Positive(t, metadataBefore)
	settings.Values[catalogconfig.NetworkMode] = "offline"
	store = openStore()
	connected, err = OpenRuntime(t.Context(), store, settings, lookup)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connected.Close(context.Background())) })
	require.Equal(t, second.State.GenerationID, connected.ControlPlane().Current().GenerationID())
	checkProjection()
	retained, err := connected.accepted.Get(t.Context(), second.State.GenerationID)
	require.NoError(t, err)
	require.Equal(t, accepted.Payload, retained.Payload)
	require.Equal(t, accepted.Manifest.SourceObservations, retained.Manifest.SourceObservations)
	require.Equal(t, providerBefore, providerCalls.Load())
	require.Equal(t, metadataBefore, metadataCalls.Load())
}

func acquisitionLifecycleCatalog(t *testing.T, endpoint string) *catalogs.Catalog {
	t.Helper()
	fixture := freshnessTestCatalog(t, []freshnessModelFact{{slug: "fixture-chat", inputPer1M: 1, outputPer1M: 2}}, nil)
	provider, err := fixture.Provider("provider")
	require.NoError(t, err)
	provider.ID = "openai"
	model := provider.Models["fixture-chat@1"]
	model.ID = "fixture-chat"
	provider.Models = map[string]*catalogs.Model{model.ID: model}
	provider.Credentials = &catalogs.ProviderCredentials{
		Fields:             []catalogs.ProviderCredentialField{{ID: "api-key", Kind: catalogs.ProviderCredentialFieldSecret, Required: true, Environment: []string{"OPENAI_API_KEY"}}},
		Profiles:           []catalogs.ProviderCredentialProfile{{ID: "api-key", Primitive: catalogs.ProviderAuthenticationAPIKey, Fields: []catalogs.ProviderCredentialFieldID{"api-key"}, Placements: []catalogs.ProviderCredentialPlacement{{Field: "api-key", Kind: catalogs.ProviderCredentialPlacementHeader, Name: "Authorization", Scheme: catalogs.ProviderCredentialSchemeBearer}}}},
		CatalogAcquisition: catalogs.ProviderCredentialPlane{Required: true, Alternatives: []catalogs.ProviderCredentialProfileID{"api-key"}},
		Inference:          catalogs.ProviderCredentialPlane{Required: true, Alternatives: []catalogs.ProviderCredentialProfileID{"api-key"}},
	}
	provider.Catalog = &catalogs.ProviderCatalog{Endpoint: catalogs.ProviderEndpoint{Type: catalogs.EndpointTypeOpenAI, URL: endpoint + "/models", ProtocolOptions: catalogs.ProviderCatalogProtocolOptions{OpenAI: &catalogs.ProviderOpenAICatalogProtocolOptions{TokenPriceUnit: catalogs.ProviderTokenPriceUnitPerMillion}}, FieldMappings: []catalogs.FieldMapping{{From: "name", To: "name"}, {From: "context_window", To: "limits.context_window"}}}}
	builder := catalogs.NewEmpty()
	author := catalogs.Author{ID: "author", Name: "Author"}
	require.NoError(t, builder.SetAuthor(author))
	require.NoError(t, builder.SetAuthorModel(author.ID, catalogs.Model{ID: model.ID, Name: "Baseline", Authors: []catalogs.Author{author}, Features: model.Features}))
	require.NoError(t, builder.SetProvider(provider))
	catalog, err := builder.Build()
	require.NoError(t, err)
	return catalog
}

// acquisitionCatalogSettings selects a controlled baseline for acquisition contracts.
func acquisitionCatalogSettings(t *testing.T, endpoint string) Settings {
	t.Helper()
	source := filepath.Join(t.TempDir(), "catalog.json")
	payload, err := catalogs.EncodeCatalogPayload(acquisitionLifecycleCatalog(t, endpoint))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(source, payload, 0600))
	settings := identityTestSettings(filepath.Join(t.TempDir(), "runtime"), "", "")
	settings.Source = string(runtime.SourceFile)
	settings.SourceURL = source
	settings.SourceStartupPolicy = string(runtime.StartupRequireSource)
	settings.SourcePollInterval = 0
	return settings
}
