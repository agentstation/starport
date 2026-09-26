package cli

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/stretchr/testify/require"
)

func TestRuntimeMigrationCommands(t *testing.T) {
	for _, phase := range []string{"prepare", "stage", "publish", "complete"} {
		t.Run(phase, func(t *testing.T) {
			deps, output, _ := testDependencies()
			root := t.TempDir()
			calls := 0
			deps.MigrateRuntime = func(_ context.Context, cfg *config.Config, gotPhase string, request catalog.RuntimeMigration) (catalog.RuntimeMigrationResult, error) {
				calls++
				require.NotNil(t, cfg)
				require.Equal(t, phase, gotPhase)
				require.Equal(t, "move", request.OperationID)
				require.Equal(t, filepath.Join(root, "source"), request.SourceDirectory)
				require.Equal(t, "scheduler", request.SourceIdentity)
				return catalog.RuntimeMigrationResult{Phase: phase, TargetDirectory: request.TargetDirectory, HostJournalDirectory: filepath.Join(root, "host-journal")}, nil
			}
			err := Run(t.Context(), []string{"starport", "migrate", "runtime", phase, "--operation", "move", "--source", filepath.Join(root, "source"), "--target", filepath.Join(root, "target"), "--journal", filepath.Join(root, "journal"), "--identity", "scheduler", "--json"}, deps)
			require.NoError(t, err)
			require.Equal(t, 1, calls)
			require.Contains(t, output.String(), `"phase": "`+phase+`"`)
			require.Contains(t, output.String(), `"host_journal_directory"`)
		})
	}
}

func TestRuntimeMigrationCommandRefusals(t *testing.T) {
	for _, mode := range []string{"relative", "missing", "arguments", "storage"} {
		t.Run(mode, func(t *testing.T) {
			deps, _, _ := testDependencies()
			root := t.TempDir()
			calls := 0
			failure := errors.New("catalog store unavailable")
			deps.MigrateRuntime = func(context.Context, *config.Config, string, catalog.RuntimeMigration) (catalog.RuntimeMigrationResult, error) {
				calls++
				return catalog.RuntimeMigrationResult{}, failure
			}
			args := []string{"starport", "migrate", "runtime", "prepare", "--operation", "move", "--source", root, "--target", filepath.Join(root, "target"), "--journal", filepath.Join(root, "journal"), "--identity", "scheduler"}
			switch mode {
			case "relative":
				args[7] = "relative"
			case "missing":
				args = args[:len(args)-2]
			case "arguments":
				args = append(args, "extra")
			}
			err := Run(t.Context(), args, deps)
			require.Error(t, err)
			if mode == "storage" {
				require.ErrorIs(t, err, failure)
				require.Equal(t, 1, calls)
				require.Equal(t, ExitCodeRuntime, ExitCode(err))
			} else {
				require.Zero(t, calls)
				require.Equal(t, ExitCodeUsage, ExitCode(err))
			}
		})
	}
}
