package cli

import (
	"context"
	"encoding/json/v2"
	"errors"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/stretchr/testify/require"
)

func applyHistoryArgs(t *testing.T) []string {
	t.Helper()
	return []string{"starport", "backup", "apply-history", "--directory", t.TempDir(), "--manifest-sha256", strings.Repeat("a", 64), "--operation", "restore", "--fencing-evidence", "external-fence", "--history-directory", t.TempDir(), "--history-sha256", strings.Repeat("b", 64), "--expected-target-sha256", strings.Repeat("c", 64), "--journal-directory", t.TempDir(), "--operator", "operator", "--attestation-reference", "external-history", "--writers-fenced=true", "--admitted-work-accounted=true"}
}

func TestApplyHistoryCommandRequiresEvidenceBeforeConfiguration(t *testing.T) {
	for _, mode := range []string{"history-digest", "target-digest", "relative-journal", "missing-fence", "unaccounted-work", "extra-argument"} {
		t.Run(mode, func(t *testing.T) {
			deps, _, _ := testDependencies()
			deps.LoadConfig = func(context.Context) (*config.Config, error) {
				t.Fatal("invalid history request loaded configuration")
				return nil, nil
			}
			args := applyHistoryArgs(t)
			for i, value := range args {
				if mode == "history-digest" && value == "--history-sha256" || mode == "target-digest" && value == "--expected-target-sha256" {
					args[i+1] = "invalid"
				}
				if mode == "relative-journal" && value == "--journal-directory" {
					args[i+1] = "relative"
				}
				if mode == "missing-fence" && value == "--writers-fenced=true" {
					args[i] = "--writers-fenced=false"
				}
				if mode == "unaccounted-work" && value == "--admitted-work-accounted=true" {
					args[i] = "--admitted-work-accounted=false"
				}
			}
			if mode == "extra-argument" {
				args = append(args, "unexpected")
			}
			err := Run(t.Context(), args, deps)
			require.Error(t, err)
			require.Equal(t, ExitCodeUsage, ExitCode(err))
		})
	}
}

func TestApplyHistoryCommandPreservesAttestationsAndClosedReport(t *testing.T) {
	for _, structured := range []bool{false, true} {
		t.Run(map[bool]string{false: "human", true: "json"}[structured], func(t *testing.T) {
			deps, out, _ := testDependencies()
			args := append(applyHistoryArgs(t), "--complete-interval")
			calls := 0
			deps.ApplyImportedHistory = func(_ context.Context, _ *config.Config, request recovery.ApplyHistoryRequest) (recovery.HistoryReplayReport, error) {
				calls++
				require.True(t, request.Attestation.WritersFenced)
				require.True(t, request.Attestation.AdmittedWorkAccounted)
				require.True(t, request.Attestation.CompleteInterval)
				require.Equal(t, "external-fence", request.Operation.FencingEvidence)
				require.Equal(t, "external-history", request.Attestation.Reference)
				require.Equal(t, strings.Repeat("c", 64), request.ExpectedTargetSHA256)
				return recovery.HistoryReplayReport{CompletedSteps: 2, DeclaredSteps: 2, Restricted: true, JournalSHA256: strings.Repeat("d", 64)}, nil
			}
			if structured {
				args = append(args, "--json")
			}
			require.NoError(t, Run(t.Context(), args, deps))
			require.Equal(t, 1, calls)
			if structured {
				var result recovery.HistoryReplayReport
				require.NoError(t, json.Unmarshal(out.Bytes(), &result))
				require.True(t, result.Restricted)
				require.Equal(t, 2, result.CompletedSteps)
			} else {
				require.Contains(t, out.String(), "All admission remains closed")
			}
			require.NotContains(t, out.String(), "external-history")
		})
	}
}

func TestApplyHistoryCommandFailureHasNoSuccessReceipt(t *testing.T) {
	deps, out, _ := testDependencies()
	failure := errors.New("native history conflict")
	deps.ApplyImportedHistory = func(context.Context, *config.Config, recovery.ApplyHistoryRequest) (recovery.HistoryReplayReport, error) {
		return recovery.HistoryReplayReport{CompletedSteps: 2}, failure
	}
	require.ErrorIs(t, Run(t.Context(), append(applyHistoryArgs(t), "--json"), deps), failure)
	require.Empty(t, out.String())
}
