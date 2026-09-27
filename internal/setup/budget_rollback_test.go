package setup

import (
	"testing"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/stretchr/testify/require"
)

func TestRollbackRefusesChangedBudgetIdentity(t *testing.T) {
	for _, mode := range []string{"missing", "changed", "additional-state"} {
		t.Run(mode, func(t *testing.T) {
			paths := productSetupPaths(t)
			service := New(paths)
			result, err := service.Initialize(t.Context(), Request{APIKeyName: "local-admin"})
			require.NoError(t, err)
			store, err := openLocalStore(paths.BadgerDir)
			require.NoError(t, err)
			marker, err := reservation.FreshHolderIdentity(limits.ScopeKey, result.apiKeyID)
			require.NoError(t, err)
			switch mode {
			case "missing":
				err = store.Delete(t.Context(), marker.Key)
			case "changed":
				err = store.Set(t.Context(), marker.Key, []byte(`{"scope":"key","id":"different"}`))
			case "additional-state":
				err = store.Set(t.Context(), "budget:v1:unexpected", []byte("preserve"))
			}
			require.NoError(t, err)
			require.NoError(t, store.Close())
			require.ErrorIs(t, service.Rollback(t.Context(), result), ErrRollbackRefused)
			require.FileExists(t, paths.ConfigFile)
			require.DirExists(t, paths.BadgerDir)
		})
	}
}
