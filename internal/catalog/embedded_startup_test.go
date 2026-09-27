package catalog

import (
	"context"
	"path/filepath"
	"testing"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestOfflineEmbeddedCandidateHasDurableGeneration(t *testing.T) {
	settings := identityTestSettings(filepath.Join(t.TempDir(), "runtime"), "", "")
	settings.Values = map[string]string{catalogconfig.NetworkMode: "offline"}
	connected, err := OpenRuntime(t.Context(), storage.NewMockStore(), settings, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connected.Close(context.Background())) })
	candidate, err := connected.CurrentCandidate(t.Context())
	require.NoError(t, err)
	_, err = connected.candidates.Get(t.Context(), candidate.State.GenerationID)
	require.NoError(t, err, "the embedded candidate needs durable bytes before acceptance")
	require.NoError(t, connected.Accept(t.Context(), candidate))
	require.Equal(t, RouteValidationAccepted, connected.RouteValidation().State)
}
