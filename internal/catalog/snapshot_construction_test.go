package catalog

import (
	"testing"

	"github.com/agentstation/starmap"
	"github.com/stretchr/testify/require"
)

func mustRoutableSnapshot(t testing.TB, state starmap.CatalogState, revision uint64, routes []Route, routability []OfferingRoutability) *RoutableSnapshot {
	t.Helper()
	snapshot, err := newRoutableSnapshot(state, revision, routes, routability)
	require.NoError(t, err)
	return snapshot
}
