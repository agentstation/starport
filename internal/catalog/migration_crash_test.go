package catalog

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

const migrationCrashExit = 73

func TestMigrationJournalProcessRecovery(t *testing.T) {
	if root := os.Getenv("STARPORT_TEST_MIGRATION_CRASH_ROOT"); root != "" {
		mode := os.Getenv("STARPORT_TEST_MIGRATION_CRASH_POINT")
		settings := identityTestSettings(filepath.Join(root, "source"), "", "")
		migration := RuntimeMigration{OperationID: "crash", JournalRoot: filepath.Join(root, "journal")}
		store := authoritySnapshotBadger(t, filepath.Join(root, "badger"))
		generations, err := NewGenerationStore(store)
		require.NoError(t, err)
		generation := runtimeTestGeneration(t, "retained", testEmptyCatalog(t, "fixture"), time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
		require.NoError(t, generations.Commit(t.Context(), generation, ""))
		intercepted := migrationCrashStore{KVStore: store, key: migration.storeReceiptKey(settings), after: mode == "after-checkpoint"}
		require.NoError(t, migration.bindStore(t.Context(), intercepted, settings))
		t.Fatal("migration did not reach the selected crash point")
	}
	for _, point := range []string{"before-checkpoint", "after-checkpoint"} {
		t.Run(point, func(t *testing.T) {
			root := t.TempDir()
			executable, err := os.Executable()
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, executable, "-test.run=^TestMigrationJournalProcessRecovery$")
			child.Env = append(os.Environ(), "STARPORT_TEST_MIGRATION_CRASH_ROOT="+root, "STARPORT_TEST_MIGRATION_CRASH_POINT="+point)
			output, err := child.CombinedOutput()
			var exited *exec.ExitError
			require.ErrorAs(t, err, &exited, string(output))
			require.Equal(t, migrationCrashExit, exited.ExitCode(), string(output))
			settings := identityTestSettings(filepath.Join(root, "source"), "", "")
			migration := RuntimeMigration{OperationID: "crash", JournalRoot: filepath.Join(root, "journal")}
			directory, err := migration.bindingDirectory(settings, false)
			require.NoError(t, err)
			journal, err := directory.ReadFile("catalog-binding.json", 1<<20)
			require.NoError(t, err)
			store := authoritySnapshotBadger(t, filepath.Join(root, "badger"))
			_, err = store.Get(t.Context(), migration.storeReceiptKey(settings))
			if point == "before-checkpoint" {
				require.ErrorIs(t, err, storage.ErrNotFound)
			} else {
				require.NoError(t, err)
			}
			require.Error(t, migration.bindStore(t.Context(), storage.NewMockStore(), settings), "a crash must not permit another store to claim the operation")
			require.NoError(t, migration.bindStore(t.Context(), store, settings))
			restored, err := store.Get(t.Context(), migration.storeReceiptKey(settings))
			require.NoError(t, err)
			require.Equal(t, journal, restored)
			generations, err := NewGenerationStore(store)
			require.NoError(t, err)
			current, err := generations.Current(t.Context())
			require.NoError(t, err)
			require.Equal(t, "retained", current.Manifest.GenerationID)
		})
	}
}

type migrationCrashStore struct {
	storage.KVStore
	key   string
	after bool
}

func (s migrationCrashStore) CompareAndSwap(ctx context.Context, key string, old, next []byte) error {
	if key == s.key && !s.after {
		os.Exit(migrationCrashExit)
	}
	err := s.KVStore.CompareAndSwap(ctx, key, old, next)
	if key == s.key && s.after && err == nil {
		os.Exit(migrationCrashExit)
	}
	return err
}
