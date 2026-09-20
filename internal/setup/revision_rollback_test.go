package setup

import (
	"path/filepath"
	"testing"

	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/config"
	"github.com/stretchr/testify/require"
)

func TestRollbackRefusesChangedAuthorizationRevision(t *testing.T) {
	for _, marker := range []string{"", "invalid", `{"epoch":"fixture","sequence":2}`} {
		t.Run(marker, func(t *testing.T) {
			paths := config.PathsForConfigDir(filepath.Join(t.TempDir(), "starport"))
			service := New(paths)
			result, err := service.Initialize(t.Context(), Request{APIKeyName: "local-admin"})
			require.NoError(t, err)
			store, err := openLocalStore(paths.BadgerDir)
			require.NoError(t, err)
			if marker == "" {
				err = store.Delete(t.Context(), revision.StorageKey)
			} else {
				err = store.Set(t.Context(), revision.StorageKey, []byte(marker))
			}
			closeErr := store.Close()
			require.NoError(t, err)
			require.NoError(t, closeErr)
			require.ErrorIs(t, service.validateRollbackRecords(t.Context(), paths.BadgerDir, result.apiKeyID), ErrRollbackRefused)
			require.ErrorIs(t, service.Rollback(t.Context(), result), ErrRollbackRefused)
			require.FileExists(t, paths.ConfigFile)
			require.DirExists(t, paths.BadgerDir)
		})
	}
}
