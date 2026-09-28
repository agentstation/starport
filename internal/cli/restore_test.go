package cli

import (
	"context"
	"errors"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"strings"
	"testing"
)

func TestRestorePrepareCommandExplainsRestrictedOutput(t *testing.T) {
	deps, output, _ := testDependencies()
	require.NoError(t, Run(t.Context(), []string{"starport", "backup", "prepare", "--help"}, deps))
	require.Contains(t, output.String(), "does not approve admission")
	require.Contains(t, output.String(), "--fencing-evidence")
	require.Contains(t, output.String(), "--files-directory")
}

func TestRestorePrepareRejectsInvalidRequestsBeforeConfiguration(t *testing.T) {
	for _, mode := range []string{"relative-backup", "bad-digest", "relative-files", "empty-evidence", "empty-operation", "extra-argument"} {
		t.Run(mode, func(t *testing.T) {
			deps, _, _ := testDependencies()
			deps.LoadConfig = func(context.Context) (*config.Config, error) {
				t.Fatal("invalid restore loaded configuration")
				return nil, nil
			}
			directory, files, digest, evidence, operation := t.TempDir(), filepath.Join(t.TempDir(), "inactive"), strings.Repeat("a", 64), "external-proof", "restore"
			switch mode {
			case "relative-backup":
				directory = "relative"
			case "bad-digest":
				digest = "bad"
			case "relative-files":
				files = "relative"
			case "empty-evidence":
				evidence = " "
			case "empty-operation":
				operation = " "
			}
			args := []string{"starport", "backup", "prepare", "--directory", directory, "--manifest-sha256", digest, "--files-directory", files, "--operation", operation, "--fencing-evidence", evidence}
			if mode == "extra-argument" {
				args = append(args, "extra")
			}
			err := Run(t.Context(), args, deps)
			require.Error(t, err)
			require.Equal(t, ExitCodeUsage, ExitCode(err))
		})
	}
}

func TestRestorePrepareFailureDoesNotPrintReceipt(t *testing.T) {
	deps, output, _ := testDependencies()
	failure := errors.New("import interrupted")
	deps.PrepareBackup = func(context.Context, *config.Config, recovery.PrepareRequest) (recovery.PrepareResult, error) {
		return recovery.PrepareResult{Prepared: recovery.PreparedBundle{OperationID: "must-not-appear"}}, failure
	}
	err := Run(t.Context(), []string{"starport", "backup", "prepare", "--directory", t.TempDir(), "--manifest-sha256", strings.Repeat("a", 64), "--files-directory", filepath.Join(t.TempDir(), "inactive"), "--operation", "restore", "--fencing-evidence", "proof", "--json"}, deps)
	require.ErrorIs(t, err, failure)
	require.Equal(t, ExitCodeRuntime, ExitCode(err))
	require.Empty(t, output.String())
}
