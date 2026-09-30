package catalog

import (
	legacyjson "encoding/json"
	"encoding/json/v2"
	"os"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/pkg/sources"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

// This fixture-only probe writes actual accepted and candidate owner records before normal startup.
func TestStarportActivationGenerationProbe(t *testing.T) {
	path := os.Getenv("STARPORT_GENERATION_PROBE_FIXTURE")
	require.NotEmpty(t, path)
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	require.LessOrEqual(t, len(body), 1<<20)
	var fixture struct {
		Storage    storage.BadgerConfig
		Payload    []byte
		Settings   Settings
		ExportPath string
	}
	require.NoError(t, json.Unmarshal(body, &fixture, legacyjson.FormatDurationAsNano(true)))
	catalog, err := catalogs.DecodeCatalogPayload(fixture.Payload)
	require.NoError(t, err)
	generation := runtimeTestGeneration(t, "bounded-activation-fixture", catalog, time.Now().UTC())
	store, err := storage.OpenBadger(fixture.Storage)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	accepted, err := NewGenerationStore(store)
	require.NoError(t, err)
	require.NoError(t, accepted.Commit(t.Context(), generation, ""))
	candidates, err := newCandidateGenerationStore(store)
	require.NoError(t, err)
	require.NoError(t, candidates.Commit(t.Context(), generation, ""))
	target, _, err := fixture.Settings.RecoveryTopologyTarget()
	require.NoError(t, err)
	metadata, err := fixture.Settings.metadataCollector()
	require.NoError(t, err)
	exported, err := json.Marshal(struct {
		Generation catalogs.Generation
		Values     map[string]string
		Target     TopologyRuntimeTarget
		Sources    []sources.SourceActivity
	}{generation, fixture.Settings.catalogValues(), target, metadata.SourceConfiguration()}, json.Deterministic(true))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(fixture.ExportPath, exported, 0600))
}
