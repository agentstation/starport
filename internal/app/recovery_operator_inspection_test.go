package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agentstation/starport/internal/localauth"
	"github.com/stretchr/testify/require"
)

func TestRecoveryOperatorInputsInspectionNeverCreatesMissingDecision(t *testing.T) {
	cfg, db, assets, request := operatorInputFixture(t)
	inspect := InspectRecoveryOperatorInputs
	_, err := inspect(t.Context(), cfg, db, assets, request)
	require.Error(t, err, "passive inspection cannot create missing activation evidence")
	require.NoFileExists(t, filepath.Join(request.JournalDirectory, recoveryOperatorInputsFile))
}

func TestRecoveryOperatorInputsInspectionReopensOriginalEvidence(t *testing.T) {
	for _, change := range []string{"none", "missing", "corrupt", "settings", "token", "operation", "directory"} {
		t.Run(change, func(t *testing.T) {
			cfg, db, assets, request := operatorInputFixture(t)
			original, err := VerifyRecoveryOperatorInputs(t.Context(), cfg, db, assets, request)
			require.NoError(t, err)
			path := filepath.Join(request.JournalDirectory, recoveryOperatorInputsFile)
			body, err := os.ReadFile(path)
			require.NoError(t, err)
			switch change {
			case "missing":
				require.NoError(t, os.Remove(path))
			case "corrupt":
				require.NoError(t, os.WriteFile(path, []byte("corrupt original decision"), 0600))
			case "settings":
				cfg.Server.Port++
			case "token":
				require.NoError(t, os.WriteFile(cfg.EffectivePaths().LocalTokenFile, []byte("corrupt selected token"), 0600))
			case "operation":
				request.Operation.FencingEvidence = "different fence"
			case "directory":
				require.NoError(t, os.Rename(request.JournalDirectory, request.JournalDirectory+"-old"))
				require.NoError(t, os.Mkdir(request.JournalDirectory, 0700))
				require.NoError(t, os.WriteFile(path, body, 0600))
			}
			before, readErr := os.ReadFile(path)
			token, err := os.ReadFile(cfg.EffectivePaths().LocalTokenFile)
			require.NoError(t, err)
			inspect := InspectRecoveryOperatorInputs
			checked, err := inspect(t.Context(), cfg, db, assets, request)
			if change == "none" {
				require.NoError(t, err)
				require.Equal(t, original.Report(), checked.Report())
				require.True(t, checked.Report().Restricted)
				require.NoError(t, checked.Check(t.Context(), cfg, db, assets, request))
			} else {
				require.Error(t, err)
			}
			after, afterErr := os.ReadFile(path)
			if readErr != nil {
				require.ErrorIs(t, afterErr, os.ErrNotExist)
			} else {
				require.NoError(t, afterErr)
				require.Equal(t, before, after)
			}
			actual, err := os.ReadFile(cfg.EffectivePaths().LocalTokenFile)
			require.NoError(t, err)
			require.Equal(t, token, actual)
			if change == "token" {
				store, err := localauth.NewStore(cfg.EffectivePaths().LocalTokenFile)
				require.NoError(t, err)
				_, err = store.Peek(t.Context())
				require.Error(t, err)
			}
		})
	}
}
