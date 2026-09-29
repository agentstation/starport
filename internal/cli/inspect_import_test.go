package cli

import (
	"context"
	"encoding/json/v2"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/stretchr/testify/require"
)

func inspectionArgs(t *testing.T) []string {
	t.Helper()
	return []string{"starport", "backup", "inspect-import", "--directory", t.TempDir(), "--manifest-sha256", strings.Repeat("a", 64), "--destination", filepath.Join(t.TempDir(), "inspection"), "--operation", "restore", "--fencing-evidence", "fenced", "--expected-deployment", "fixture", "--expected-recovery-epoch", "2", "--expected-recovery-evidence", "prepared", "--kv-replay-sequence", "0", "--sql-replay-sequence", "0"}
}

func TestInspectImportCommandRefusesIncompleteRequestBeforeConfiguration(t *testing.T) {
	for _, mode := range []string{"missing-kv", "missing-sql", "negative", "missing-digest", "initial-digest", "bad-digest", "relative-output", "extra-argument"} {
		t.Run(mode, func(t *testing.T) {
			deps, _, _ := testDependencies()
			deps.LoadConfig = func(context.Context) (*config.Config, error) {
				t.Fatal("invalid request loaded configuration")
				return nil, nil
			}
			args := inspectionArgs(t)
			for i := 3; i < len(args); i += 2 {
				if mode == "missing-kv" && args[i] == "--kv-replay-sequence" || mode == "missing-sql" && args[i] == "--sql-replay-sequence" {
					args = append(args[:i], args[i+2:]...)
					break
				}
				if args[i] == "--kv-replay-sequence" && mode == "negative" {
					args[i+1] = "-1"
				}
				if args[i] == "--kv-replay-sequence" && mode == "missing-digest" {
					args[i+1] = "1"
				}
				if args[i] == "--manifest-sha256" && mode == "bad-digest" {
					args[i+1] = "bad"
				}
				if args[i] == "--destination" && mode == "relative-output" {
					args[i+1] = "relative"
				}
			}
			if mode == "initial-digest" {
				args = append(args, "--kv-replay-sha256", strings.Repeat("a", 64))
			}
			if mode == "extra-argument" {
				args = append(args, "extra")
			}
			err := Run(t.Context(), args, deps)
			require.Error(t, err)
			require.Equal(t, ExitCodeUsage, ExitCode(err))
		})
	}
}

func TestInspectImportCommandBindsExplicitZeroAndReturnsReceipt(t *testing.T) {
	for _, structured := range []bool{false, true} {
		t.Run(map[bool]string{false: "human", true: "json"}[structured], func(t *testing.T) {
			deps, out, _ := testDependencies()
			args := inspectionArgs(t)
			calls := 0
			deps.InspectImportedBackup = func(_ context.Context, _ *config.Config, r recovery.InspectImportRequest) (recovery.ImportInspectionResult, error) {
				calls++
				require.Zero(t, r.KVPosition.Sequence)
				require.Zero(t, r.SQLPosition.Sequence)
				require.Equal(t, "fenced", r.Operation.FencingEvidence)
				require.False(t, r.ExpectedBoundary.Open)
				return recovery.ImportInspectionResult{Directory: r.Destination, ManifestSHA256: r.ManifestSHA256, Operation: r.Operation, Request: recovery.ImportedReferenceRequest{Boundary: r.ExpectedBoundary}, Inspection: recovery.ImportedReferences{RequestSHA256: strings.Repeat("b", 64)}}, nil
			}
			if structured {
				args = append(args, "--json")
			}
			require.NoError(t, Run(t.Context(), args, deps))
			require.Equal(t, 1, calls)
			if structured {
				var result recovery.ImportInspectionResult
				require.NoError(t, json.Unmarshal(out.Bytes(), &result))
				require.Equal(t, "fixture", result.Request.Boundary.DeploymentID)
				require.Equal(t, strings.Repeat("b", 64), result.Inspection.RequestSHA256)
			} else {
				require.Contains(t, out.String(), "do not approve activation")
				require.NotContains(t, out.String(), "kv_claim")
			}
		})
	}
}

func TestInspectImportCommandFailureHasNoReceipt(t *testing.T) {
	deps, out, _ := testDependencies()
	failure := errors.New("changed import")
	deps.InspectImportedBackup = func(context.Context, *config.Config, recovery.InspectImportRequest) (recovery.ImportInspectionResult, error) {
		return recovery.ImportInspectionResult{Directory: "must-not-print"}, failure
	}
	require.ErrorIs(t, Run(t.Context(), append(inspectionArgs(t), "--json"), deps), failure)
	require.Empty(t, out.String())
}

func TestInspectImportHelpExplainsReceiptInputs(t *testing.T) {
	deps, out, _ := testDependencies()
	require.NoError(t, Run(t.Context(), []string{"starport", "backup", "inspect-import", "--help"}, deps))
	for _, term := range []string{"last accepted replay receipt", "explicitly set its sequence to 0", "Keep all writers fenced", "does not approve activation", "--valkey-incarnation"} {
		require.Contains(t, out.String(), term)
	}
}
