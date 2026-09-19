package catalog

import (
	"bytes"
	"context"
	"fmt"
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
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestOfflineRuntimeDefersAcquisitionCredentials(t *testing.T) {
	root := t.TempDir()
	var providerCalls, metadataCalls, credentialReads atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		http.Error(w, "unexpected acquisition", 500)
	}))
	t.Cleanup(provider.Close)
	prior := http.DefaultTransport
	http.DefaultTransport = metadataTransport(func(*http.Request) (*http.Response, error) {
		metadataCalls.Add(1)
		return nil, fmt.Errorf("unexpected metadata network access")
	})
	t.Cleanup(func() { http.DefaultTransport = prior })
	settings := identityTestSettings(filepath.Join(root, "runtime"), "", "")
	settings.Source = "file"
	settings.SourceURL = filepath.Join(root, "catalog.json")
	settings.SourceStartupPolicy = "require_source"
	settings.SourcePollInterval = 10 * time.Millisecond
	settings.AcquisitionEnabled = true
	settings.AcquisitionInterval = 10 * time.Millisecond
	settings.SourceCacheDirectory = filepath.Join(root, "cache")
	settings.Values = map[string]string{catalogconfig.NetworkMode: "offline", catalogconfig.AcquisitionSources: string(sources.ProvidersID) + "," + string(sources.ModelsDevHTTPID), catalogconfig.StartupSpread: "0s"}
	payload, err := catalogs.EncodeCatalogPayload(acquisitionLifecycleCatalog(t, provider.URL))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(settings.SourceURL, payload, 0600))
	lookup := func(string) (string, bool) { credentialReads.Add(1); return "fixture-acquisition-key", true }
	connected, err := OpenRuntime(t.Context(), storage.NewMockStore(), settings, lookup)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connected.Close(context.Background())) })
	require.Never(t, func() bool {
		return providerCalls.Load() != 0 || metadataCalls.Load() != 0 || credentialReads.Load() != 0
	}, 100*time.Millisecond, time.Millisecond)
	_, err = connected.Refresh(t.Context())
	require.Error(t, err, "manual refresh must refuse network acquisition while offline")
	require.Zero(t, providerCalls.Load())
	require.Zero(t, metadataCalls.Load())
	require.Zero(t, credentialReads.Load())
	require.NoDirExists(t, settings.SourceCacheDirectory)
}

func TestOfflineRuntimeReadsLocalFileUpdates(t *testing.T) {
	root := t.TempDir()
	settings := identityTestSettings(filepath.Join(root, "runtime"), "", "")
	settings.Source = "file"
	settings.SourceURL = filepath.Join(root, "catalog.json")
	settings.SourceStartupPolicy = "require_source"
	settings.SourcePollInterval = 0
	settings.Values = map[string]string{catalogconfig.NetworkMode: "offline", catalogconfig.AcquisitionSources: ""}
	payload, err := catalogs.EncodeCatalogPayload(acquisitionLifecycleCatalog(t, "https://provider.invalid"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(settings.SourceURL, payload, 0600))
	store := authoritySnapshotBadger(t, filepath.Join(root, "badger"))
	connected, err := OpenRuntime(t.Context(), store, settings, func(string) (string, bool) {
		t.Error("local file import resolved an acquisition credential")
		return "", false
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connected.Close(context.Background())) })
	updated := bytes.ReplaceAll(payload, []byte("fixture-chat"), []byte("fixture-next"))
	require.NotEqual(t, payload, updated)
	require.NoError(t, os.WriteFile(settings.SourceURL, updated, 0600))
	_, err = connected.Refresh(t.Context())
	require.NoError(t, err)
	candidate, err := connected.CurrentCandidate(t.Context())
	require.NoError(t, err)
	provider, err := candidate.State.Catalog.Provider("openai")
	require.NoError(t, err)
	require.Contains(t, provider.Models, "fixture-next")
	require.NoError(t, connected.Accept(t.Context(), candidate))
	require.NoError(t, connected.Close(t.Context()))
	require.NoError(t, store.Close())
	store = authoritySnapshotBadger(t, filepath.Join(root, "badger"))
	restarted, err := OpenRuntime(t.Context(), store, settings, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restarted.Close(context.Background())) })
	require.Equal(t, candidate.State.GenerationID, restarted.ControlPlane().Current().GenerationID())
}

func TestManualSourceRetainsAutomaticProviderAcquisition(t *testing.T) {
	root := t.TempDir()
	var calls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer fixture-key" {
			http.Error(w, "wrong credential", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"fixture-chat","name":"Automatic provider observation","context_window":12345}]}`))
	}))
	t.Cleanup(provider.Close)
	settings := identityTestSettings(filepath.Join(root, "runtime"), "", "")
	settings.Source = "file"
	settings.SourceURL = filepath.Join(root, "catalog.json")
	settings.SourceStartupPolicy = "require_source"
	settings.SourcePollInterval = 10 * time.Millisecond
	settings.AcquisitionEnabled = false
	settings.Values = map[string]string{catalogconfig.AcquisitionSources: string(sources.ProvidersID), catalogconfig.StartupSpread: "0s"}
	payload, err := catalogs.EncodeCatalogPayload(acquisitionLifecycleCatalog(t, provider.URL))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(settings.SourceURL, payload, 0600))
	store := authoritySnapshotBadger(t, filepath.Join(root, "badger"))
	connected, err := OpenRuntime(t.Context(), store, settings, nil)
	require.NoError(t, err)
	initial, err := connected.CurrentCandidate(t.Context())
	require.NoError(t, err)
	require.NoError(t, connected.Accept(t.Context(), initial))
	require.NoError(t, connected.Close(t.Context()))
	require.NoError(t, store.Close())
	settings.Values[catalogconfig.SourceRefreshMode] = "manual"
	settings.AcquisitionEnabled = true
	settings.AcquisitionInterval = time.Hour
	require.NoError(t, os.WriteFile(settings.SourceURL, bytes.ReplaceAll(payload, []byte("fixture-chat"), []byte("fixture-next")), 0600))
	store = authoritySnapshotBadger(t, filepath.Join(root, "badger"))
	restarted, err := OpenRuntime(t.Context(), store, settings, func(name string) (string, bool) {
		if name == "STARPORT_OPENAI_API_KEY" {
			return "fixture-key", true
		}
		return "", false
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restarted.Close(context.Background())) })
	require.Eventually(t, func() bool {
		candidate, err := restarted.CurrentCandidate(t.Context())
		if err != nil {
			return false
		}
		found, err := candidate.State.Catalog.Provider("openai")
		if err != nil {
			return false
		}
		model := found.Models["fixture-chat"]
		return model != nil && model.Name == "Automatic provider observation"
	}, time.Minute, 10*time.Millisecond)
	require.Positive(t, calls.Load())
	before, err := restarted.CurrentCandidate(t.Context())
	require.NoError(t, err)
	observed, err := before.State.Catalog.Provider("openai")
	require.NoError(t, err)
	require.NotContains(t, observed.Models, "fixture-next", "manual source mode must retain the prior file across restart")
	_, err = restarted.Refresh(t.Context())
	require.NoError(t, err)
	after, err := restarted.CurrentCandidate(t.Context())
	require.NoError(t, err)
	observed, err = after.State.Catalog.Provider("openai")
	require.NoError(t, err)
	require.Contains(t, observed.Models, "fixture-next", "explicit refresh may read the selected file")
}

func TestGenerationPinBlocksAcquisitionAcrossRestart(t *testing.T) {
	root := t.TempDir()
	var calls, credentials atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "pin must prohibit acquisition", 500)
	}))
	t.Cleanup(server.Close)
	settings := identityTestSettings(filepath.Join(root, "runtime"), "", "")
	settings.Source = "file"
	settings.SourceURL = filepath.Join(root, "catalog.json")
	settings.SourceStartupPolicy = "require_source"
	settings.SourcePollInterval = 10 * time.Millisecond
	settings.Values = map[string]string{catalogconfig.AcquisitionSources: string(sources.ProvidersID), catalogconfig.StartupSpread: "0s"}
	payload, err := catalogs.EncodeCatalogPayload(acquisitionLifecycleCatalog(t, server.URL))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(settings.SourceURL, payload, 0600))
	storePath := filepath.Join(root, "badger")
	store := authoritySnapshotBadger(t, storePath)
	initial, err := OpenRuntime(t.Context(), store, settings, nil)
	require.NoError(t, err)
	selected, err := initial.CurrentCandidate(t.Context())
	require.NoError(t, err)
	require.NoError(t, initial.Accept(t.Context(), selected))
	require.NoError(t, os.WriteFile(settings.SourceURL, bytes.ReplaceAll(payload, []byte("fixture-chat"), []byte("fixture-next")), 0600))
	_, err = initial.runtime.RefreshSource(t.Context())
	require.NoError(t, err)
	unaccepted, err := initial.CurrentCandidate(t.Context())
	require.NoError(t, err)
	require.NotEqual(t, selected.State.GenerationID, unaccepted.State.GenerationID)
	require.NoError(t, initial.Close(t.Context()))
	require.NoError(t, store.Close())
	settings.Values[catalogconfig.GenerationPin] = selected.State.GenerationID
	settings.AcquisitionEnabled = true
	settings.AcquisitionInterval = 10 * time.Millisecond
	require.NoError(t, os.WriteFile(settings.SourceURL, bytes.ReplaceAll(payload, []byte("fixture-chat"), []byte("fixture-next")), 0600))
	for restart := range 2 {
		store = authoritySnapshotBadger(t, storePath)
		pinned, err := OpenRuntime(t.Context(), store, settings, func(string) (string, bool) { credentials.Add(1); return "fixture-key", true })
		require.NoError(t, err)
		require.Equal(t, selected.State.GenerationID, pinned.Status().GenerationPin)
		require.Error(t, pinned.Accept(t.Context(), unaccepted), "a candidate prepared before pinning must not bypass the selected pin")
		foreign, err := pinned.candidates.Get(t.Context(), unaccepted.State.GenerationID)
		require.NoError(t, err)
		foreign.Manifest.GenerationID = fmt.Sprintf("foreign-after-pin-%d", restart)
		foreign.Manifest.GeneratedAt = time.Now().UTC().Add(time.Second)
		current, err := pinned.candidates.Current(t.Context())
		require.NoError(t, err)
		require.NoError(t, pinned.candidates.Commit(t.Context(), foreign, current.Manifest.GenerationID))
		active, err := pinned.CurrentCandidate(t.Context())
		require.NoError(t, err)
		require.NoError(t, pinned.Accept(t.Context(), active), "the selected pinned candidate remains acceptable")
		proposal := unaccepted
		proposal.Epoch = active.Epoch
		proposal.State.GenerationID = foreign.Manifest.GenerationID
		proposal.State.GeneratedAt = foreign.Manifest.GeneratedAt
		require.Error(t, pinned.Accept(t.Context(), proposal), "a newer shared-store candidate must not bypass the local generation pin")
		require.NoError(t, pinned.candidates.Commit(t.Context(), current, foreign.Manifest.GenerationID))
		require.Never(t, func() bool { return calls.Load() != 0 || credentials.Load() != 0 }, 100*time.Millisecond, time.Millisecond)
		_, err = pinned.Refresh(t.Context())
		require.Error(t, err)
		retained, err := pinned.CurrentCandidate(t.Context())
		require.NoError(t, err)
		require.Equal(t, selected.State.PayloadChecksum, retained.State.PayloadChecksum)
		require.Zero(t, calls.Load())
		require.Zero(t, credentials.Load())
		require.NoError(t, pinned.Close(t.Context()))
		require.NoError(t, store.Close())
	}
}
