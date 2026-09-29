package cli

import (
	"context"
	"errors"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRestorePublishFilesCommandExplainsRestrictedOutput(t *testing.T) {
	deps, output, _ := testDependencies()
	require.NoError(t, Run(t.Context(), []string{"starport", "backup", "publish-files", "--help"}, deps))
	require.Contains(t, output.String(), "does not approve admission")
	require.Contains(t, output.String(), "--role")
	require.Contains(t, output.String(), "--fencing-evidence")
}

func TestRestorePublishFilesRejectsBeforeConfiguration(t *testing.T) {
	for _, mode := range []string{"unsupported-role", "empty-role", "empty-evidence", "extra-argument"} {
		t.Run(mode, func(t *testing.T) {
			deps, _, _ := testDependencies()
			deps.LoadConfig = func(context.Context) (*config.Config, error) {
				t.Fatal("invalid request loaded configuration")
				return nil, nil
			}
			role, evidence := config.InferenceCredentialPolicyRole, "external-proof"
			switch mode {
			case "unsupported-role":
				role = "local-token"
			case "empty-role":
				role = " "
			case "empty-evidence":
				evidence = " "
			}
			args := []string{"starport", "backup", "publish-files", "--directory", t.TempDir(), "--manifest-sha256", strings.Repeat("a", 64), "--files-directory", filepath.Join(t.TempDir(), "prepared"), "--operation", "restore", "--fencing-evidence", evidence, "--role", role}
			if mode == "extra-argument" {
				args = append(args, "extra")
			}
			err := Run(t.Context(), args, deps)
			require.Error(t, err)
			require.Equal(t, ExitCodeUsage, ExitCode(err))
		})
	}
}

func TestRestorePublishFilesErrorDoesNotPrintSuccessReceipt(t *testing.T) {
	deps, output, _ := testDependencies()
	failure := errors.New("publication durability uncertain")
	deps.PublishBackupFiles = func(context.Context, *config.Config, recovery.PublishFilesRequest) (recovery.PublishFilesResult, error) {
		return recovery.PublishFilesResult{Role: "must-not-appear", Tree: recovery.FileTreeResult{Published: true}}, failure
	}
	err := Run(t.Context(), []string{"starport", "backup", "publish-files", "--directory", t.TempDir(), "--manifest-sha256", strings.Repeat("a", 64), "--files-directory", filepath.Join(t.TempDir(), "prepared"), "--operation", "restore", "--fencing-evidence", "proof", "--role", config.InferenceCredentialPolicyRole, "--json"}, deps)
	require.ErrorIs(t, err, failure)
	require.Equal(t, ExitCodeRuntime, ExitCode(err))
	require.Empty(t, output.String())
}
