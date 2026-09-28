package cli

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/stretchr/testify/require"
)

func TestBackupCommandRejectsInvalidRequestsBeforeConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backup")
	for _, args := range [][]string{
		{"close", "extra"},
		{"create", "--destination", path, "--operation", "one", "--fencing-evidence", "proof"},
		{"create", "--destination", "relative", "--operation", "one", "--fencing-evidence", "proof", "--key-reference", "key"},
		{"create", "--destination", path, "--operation", "one", "--fencing-evidence", " ", "--key-reference", "key"},
		{"create", "--destination", path, "--operation", "one", "--fencing-evidence", "proof", "--key-reference", "key", "--entry-limit", "100001"},
		{"verify", "--directory", path, "--manifest-sha256", "invalid"},
		{"verify", "--directory", "relative", "--manifest-sha256", strings.Repeat("a", 64)},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			deps, _, _ := testDependencies()
			deps.LoadConfig = func(context.Context) (*config.Config, error) {
				t.Fatal("invalid request loaded configuration")
				return nil, nil
			}
			err := Run(t.Context(), append([]string{"starport", "backup"}, args...), deps)
			require.Error(t, err)
			require.Equal(t, ExitCodeUsage, ExitCode(err))
		})
	}
}

func TestBackupCommandReportsRuntimeFailureWithoutReceipt(t *testing.T) {
	deps, output, _ := testDependencies()
	failure := errors.New("capture interrupted")
	deps.CaptureBackup = func(context.Context, *config.Config, recovery.CaptureRequest) (recovery.CaptureResult, error) {
		return recovery.CaptureResult{ManifestSHA256: "must not appear"}, failure
	}
	err := Run(t.Context(), []string{"starport", "backup", "create", "--destination", filepath.Join(t.TempDir(), "backup"), "--operation", "one", "--fencing-evidence", "proof", "--key-reference", "key", "--json"}, deps)
	require.ErrorIs(t, err, failure)
	require.Equal(t, ExitCodeRuntime, ExitCode(err))
	require.Empty(t, output.String())
}

func TestBackupCommandsExposeRecoveryLimits(t *testing.T) {
	for _, verb := range []string{"close", "create", "verify"} {
		t.Run(verb, func(t *testing.T) {
			deps, output, _ := testDependencies()
			require.NoError(t, Run(t.Context(), []string{"starport", "backup", verb, "--help"}, deps))
			switch verb {
			case "close":
				require.Contains(t, output.String(), "separately stop and fence")
			case "create":
				require.Contains(t, output.String(), "--fencing-evidence")
				require.Contains(t, output.String(), "never the key value")
			case "verify":
				require.Contains(t, output.String(), "does not approve recovery")
				require.Contains(t, output.String(), "independently")
			}
		})
	}
}
