package app

import (
	"errors"
	"testing"

	"github.com/agentstation/starmap"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/stretchr/testify/require"
)

func TestMatchingCandidateStillRequiresDurableAcceptance(t *testing.T) {
	fixture := newRuntimeRefreshFixture(t)
	before := fixture.registry.Snapshot()
	failure := errors.New("acceptance unavailable")
	updates := &recordingCatalogRuntime{err: failure}
	fixture.application.catalogRuntime = updates
	candidate := runtimecatalog.Candidate{State: starmap.CatalogState{
		Catalog: before.Catalog(), GenerationID: before.GenerationID(),
		PayloadChecksum: before.PayloadChecksum(), GeneratedAt: before.GeneratedAt(),
		Sequence: before.CatalogSequence(),
	}}
	err := fixture.application.activateRuntimeState(t.Context(), candidate)
	require.ErrorIs(t, err, failure, "matching in-memory bytes do not prove durable acceptance")
	require.Same(t, before, fixture.registry.Snapshot())
	require.Equal(t, int32(1), updates.accepted.Load())
	require.Equal(t, int32(1), updates.rejected.Load())
}
