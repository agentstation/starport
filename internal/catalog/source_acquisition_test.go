package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starmap/pkg/sources"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestRuntimeAcquiresSelectedMetadataSource(t *testing.T) {
	var calls atomic.Int64
	payload := metadataSourcePayload(t)
	prior := http.DefaultTransport
	http.DefaultTransport = metadataTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://models.dev/api.json" {
			return nil, fmt.Errorf("unexpected network request: %s", request.URL.Host)
		}
		calls.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(payload)), Request: request}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = prior })
	settings := identityTestSettings(filepath.Join(t.TempDir(), "runtime"), "", "")
	settings.SourceCacheDirectory = filepath.Join(t.TempDir(), "source-cache")
	settings.AcquisitionEnabled = false
	settings.Values = map[string]string{catalogconfig.AcquisitionSources: string(sources.ModelsDevHTTPID)}
	store := storage.NewMockStore()
	connected, err := OpenRuntime(t.Context(), store, settings, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connected.Close(context.Background())) })
	require.Zero(t, calls.Load(), "disabled automatic acquisition must remain passive")
	initial, err := connected.CurrentCandidate(t.Context())
	require.NoError(t, err)
	baselineProvider, err := initial.State.Catalog.Provider("openai")
	require.NoError(t, err)
	report, err := connected.Refresh(t.Context())
	require.NoError(t, err)
	require.Positive(t, calls.Load(), "selected non-provider source must reach its collector")
	stored, err := os.ReadFile(filepath.Join(settings.SourceCacheDirectory, "models.dev", "api.json"))
	require.NoError(t, err)
	require.JSONEq(t, string(payload), string(stored))
	require.NotEmpty(t, report.Acquisition.SourceObservations)
	for _, observation := range report.Acquisition.SourceObservations {
		require.EqualValues(t, sources.ModelsDevHTTPID, observation.Source)
		require.EqualValues(t, sources.ObservationStatusDegraded, observation.Status, "this fixture omits previously attributed baseline records")
	}
	candidate, err := connected.CurrentCandidate(t.Context())
	require.NoError(t, err)
	updatedProvider, err := candidate.State.Catalog.Provider("openai")
	require.NoError(t, err)
	for id := range baselineProvider.Models {
		require.Contains(t, updatedProvider.Models, id, "source omissions must retain baseline models")
	}
	require.NoError(t, connected.Accept(t.Context(), candidate))
	accepted, err := connected.accepted.Get(t.Context(), candidate.State.GenerationID)
	require.NoError(t, err)
	require.Equal(t, report.Acquisition.SourceObservations, accepted.Manifest.SourceObservations)
	require.NoError(t, connected.Close(t.Context()))
	beforeRestart := calls.Load()
	settings.Values[catalogconfig.NetworkMode] = "offline"
	reopened, err := OpenRuntime(t.Context(), store, settings, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close(context.Background())) })
	require.Equal(t, candidate.State.GenerationID, reopened.ControlPlane().Current().GenerationID())
	require.Equal(t, beforeRestart, calls.Load())
}

func TestMetadataCollectorConstructionIsPassiveAndChecksPaths(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "cache")
	settings := Settings{SourceCacheDirectory: cache}
	_, err := settings.metadataCollector()
	require.NoError(t, err)
	require.NoDirExists(t, cache)
	settings.WorkspacePath = filepath.Join(cache, "workspace")
	_, err = settings.metadataCollector()
	require.Error(t, err)
	require.NoDirExists(t, cache)
	settings = Settings{SourceCacheDirectory: "relative"}
	_, err = settings.metadataCollector()
	require.Error(t, err)
}

func TestMetadataAcquisitionRespectsOfflineAndEmptySelection(t *testing.T) {
	for _, mode := range []string{"offline", "empty-selection"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int64
			prior := http.DefaultTransport
			http.DefaultTransport = metadataTransport(func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				return nil, fmt.Errorf("network is forbidden")
			})
			t.Cleanup(func() { http.DefaultTransport = prior })
			settings := identityTestSettings(filepath.Join(t.TempDir(), "state"), "", "")
			settings.SourceCacheDirectory = filepath.Join(t.TempDir(), "cache")
			settings.Values = map[string]string{catalogconfig.AcquisitionSources: string(sources.ModelsDevHTTPID)}
			if mode == "offline" {
				settings.Values[catalogconfig.NetworkMode] = "offline"
			} else {
				settings.Values[catalogconfig.AcquisitionSources] = ""
			}
			connected, err := OpenRuntime(t.Context(), storage.NewMockStore(), settings, nil)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, connected.Close(context.Background())) })
			_, err = connected.Refresh(t.Context())
			if mode == "offline" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Zero(t, calls.Load())
			require.NoDirExists(t, settings.SourceCacheDirectory)
		})
	}
}

type metadataTransport func(*http.Request) (*http.Response, error)

func (f metadataTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func metadataSourcePayload(t *testing.T) []byte {
	t.Helper()
	providers := make(map[string]any)
	for _, provider := range []string{"openai", "anthropic", "google", "xai", "groq"} {
		models := make(map[string]any)
		for index := range 20 {
			id := fmt.Sprintf("fixture-model-%02d", index)
			if index == 0 {
				id = "gpt-image-2"
			}
			models[id] = map[string]any{"id": id, "name": "Fixture model", "description": strings.Repeat("Source metadata fixture. ", 100), "limit": map[string]any{"context": 32768, "output": 4096}}
		}
		providers[provider] = map[string]any{"id": provider, "name": provider, "models": models}
	}
	payload, err := json.Marshal(providers)
	require.NoError(t, err)
	return payload
}
