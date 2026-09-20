package setup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestSetupCrashHelper(t *testing.T) {
	if os.Getenv("STARPORT_SETUP_TEST_CHILD") != "1" {
		return
	}
	var paths config.Paths
	require.NoError(t, json.Unmarshal([]byte(os.Getenv("STARPORT_SETUP_TEST_PATHS")), &paths))
	service := New(paths)
	phase := os.Getenv("STARPORT_SETUP_TEST_PHASE")
	service.checkpoint = func(current string) error {
		if current == phase {
			os.Exit(73)
		}
		return nil
	}
	result, err := service.Initialize(t.Context(), Request{APIKeyName: "initial-admin"})
	require.NoError(t, err)
	if phase == "rollback-restored" {
		service.openStore = func(string) (storage.KVStore, error) { return nil, errors.New("test open failure") }
	}
	require.NoError(t, service.Rollback(t.Context(), result))
	t.Fatalf("did not terminate at %s", phase)
}

func TestSetupRecoversProcessTermination(t *testing.T) {
	for _, phase := range []string{"preparing", "prepared", "data-published", "configuration-published",
		"rollback-isolated", "rollback-verified", "rollback-config-removed", "rollback-file-removed", "rollback-directory-removed", "rollback-restored"} {
		t.Run(phase, func(t *testing.T) {
			paths := productSetupPaths(t)
			encoded, err := json.Marshal(paths)
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSetupCrashHelper$")
			command.Env = append(os.Environ(), "STARPORT_SETUP_TEST_CHILD=1", "STARPORT_SETUP_TEST_PATHS="+string(encoded),
				"STARPORT_SETUP_TEST_PHASE="+phase)
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit, "%s", output)
			require.Equal(t, 73, exit.ExitCode(), "%s", output)
			_, err = GuardLocalStorage(t.Context(), config.Paths{BadgerDir: paths.BadgerDir})
			require.ErrorIs(t, err, ErrPartialState)

			service := New(paths)
			var retained setupJournal
			require.NoError(t, json.Unmarshal(mustReadFile(t, filepath.Join(paths.ConfigDir, setupMetadataDirectory, setupJournalFile)), &retained))
			result, err := service.Initialize(t.Context(), Request{APIKeyName: "replacement-admin"})
			published := phase == "configuration-published" || phase == "rollback-restored"
			if published {
				require.ErrorIs(t, err, ErrAlreadyInitialized)
			} else {
				require.NoError(t, err)
				require.NotEmpty(t, result.APIKey)
				require.NotEqual(t, retained.APIKeyID, result.apiKeyID)
			}
			_, err = service.Initialize(t.Context(), Request{APIKeyName: "third-admin"})
			require.ErrorIs(t, err, ErrAlreadyInitialized)
			require.NoFileExists(t, filepath.Join(paths.ConfigDir, setupMetadataDirectory, setupJournalFile))
			stages, err := filepath.Glob(filepath.Join(paths.DataDir, ".starport-init-*"))
			require.NoError(t, err)
			require.Empty(t, stages)
			require.FileExists(t, paths.ConfigFile)
			require.DirExists(t, paths.BadgerDir)
			store, err := openLocalStore(paths.BadgerDir)
			require.NoError(t, err)
			records, err := store.ScanWithPrefix(t.Context(), "", 0)
			require.NoError(t, err)
			require.Len(t, records, 5)
			require.NoError(t, store.Close())
		})
	}
}

func TestRollbackRestoresRefusedDatabase(t *testing.T) {
	for _, cause := range []string{"database-open", "configuration-changed", "database-changed", "cancelled", "live-database"} {
		t.Run(cause, func(t *testing.T) {
			paths := productSetupPaths(t)
			service := New(paths)
			prepared, err := service.prepareAcrossRoots(t.Context(), Request{APIKeyName: "initial-admin"})
			require.NoError(t, err)
			result, err := prepared.publish(t.Context())
			require.NoError(t, err)
			require.NoError(t, prepared.writer.close())
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if cause == "database-open" {
				service.openStore = func(string) (storage.KVStore, error) { return nil, errors.New("test open failure") }
			}
			if cause == "live-database" {
				store, err := openLocalStore(paths.BadgerDir)
				require.NoError(t, err)
				defer func() { require.NoError(t, store.Close()) }()
			}
			service.checkpoint = func(phase string) error {
				if phase != "rollback-isolated" {
					return nil
				}
				switch cause {
				case "configuration-changed":
					return os.WriteFile(paths.ConfigFile, []byte("operator change"), configFileMode)
				case "database-changed":
					return os.WriteFile(filepath.Join(paths.DataDir, prepared.journal.Stage, "operator-state"), []byte("preserve"), configFileMode)
				case "cancelled":
					cancel()
				}
				return nil
			}
			require.ErrorIs(t, service.rollbackAcrossRoots(ctx, result), ErrRollbackRefused)
			require.DirExists(t, paths.BadgerDir)
			require.FileExists(t, paths.ConfigFile)
			require.NoDirExists(t, filepath.Join(paths.DataDir, prepared.journal.Stage))
			require.NoFileExists(t, filepath.Join(paths.ConfigDir, setupMetadataDirectory, setupJournalFile))
			if cause == "configuration-changed" {
				require.Equal(t, "operator change", string(mustReadFile(t, paths.ConfigFile)))
			}
			if cause == "database-changed" {
				require.Equal(t, "preserve", string(mustReadFile(t, filepath.Join(paths.BadgerDir, "operator-state"))))
			}
		})
	}
}

func TestRecoveryPreservesChangedOrUnknownState(t *testing.T) {
	for _, change := range []string{"unknown-file", "replaced-stage", "invalid-journal", "unexpected-configuration"} {
		t.Run(change, func(t *testing.T) {
			paths := productSetupPaths(t)
			prepared, err := New(paths).prepareAcrossRoots(t.Context(), Request{APIKeyName: "initial-admin"})
			require.NoError(t, err)
			require.NoError(t, prepared.writer.close())
			stage := filepath.Join(paths.DataDir, prepared.journal.Stage)
			journal := filepath.Join(paths.ConfigDir, setupMetadataDirectory, setupJournalFile)
			switch change {
			case "unknown-file":
				require.NoError(t, os.WriteFile(filepath.Join(stage, "operator-state"), []byte("preserve"), configFileMode))
			case "replaced-stage":
				require.NoError(t, os.Rename(stage, stage+"-old"))
				require.NoError(t, os.Mkdir(stage, privateDirMode))
			case "invalid-journal":
				require.NoError(t, os.WriteFile(journal, []byte("{invalid"), configFileMode))
			case "unexpected-configuration":
				require.NoError(t, os.WriteFile(paths.ConfigFile, []byte("operator change"), configFileMode))
			}
			require.Error(t, New(paths).recoverAcrossRoots(t.Context()))
			require.DirExists(t, stage)
			require.FileExists(t, journal)
			require.NoDirExists(t, paths.BadgerDir)
			if change == "unknown-file" {
				require.Equal(t, "preserve", string(mustReadFile(t, filepath.Join(stage, "operator-state"))))
			}
		})
	}
}
