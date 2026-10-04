package cli

import (
	"context"
	"encoding/json/v2"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/stretchr/testify/require"
)

func writeHistoryArgs(t *testing.T) []string {
	t.Helper()
	evidence := filepath.Join(t.TempDir(), "source.log")
	return []string{"starport", "backup", "write-history", "--directory", t.TempDir(), "--manifest-sha256", strings.Repeat("a", 64), "--operation", "restore", "--fencing-evidence", "external-fence", "--history-directory", t.TempDir(),
		"--mode", "planned_migration", "--disposition", "replay_complete", "--through", "2026-10-03T12:00:00-07:00", "--end-reference", "source-stopped",
		"--highest-epoch", "9", "--epoch-reference", "epoch-record", "--epoch-operator", "operator", "--evidence-file", "source=" + evidence + "=archive, shelf 4", "--evidence-file", "epoch=" + evidence, "--epoch-evidence", "epoch",
		"--operator", "operator", "--attestation-reference", "external-history", "--writers-fenced=true", "--admitted-work-accounted=true", "--complete-interval=true"}
}

func TestWriteHistoryCommandRequiresEvidenceBeforeConfiguration(t *testing.T) {
	replace := map[string][2]string{
		"unfenced":            {"--writers-fenced=true", "--writers-fenced=false"},
		"unaccounted-work":    {"--admitted-work-accounted=true", "--admitted-work-accounted=false"},
		"incomplete-interval": {"--complete-interval=true", "--complete-interval=false"},
	}
	values := map[string][2]string{
		"backup-digest":     {"--manifest-sha256", "invalid"},
		"relative-history":  {"--history-directory", "relative"},
		"through-format":    {"--through", "2026-10-03 12:00"},
		"mode":              {"--mode", "automatic"},
		"disposition":       {"--disposition", "activate"},
		"evidence-format":   {"--evidence-file", "source"},
		"relative-evidence": {"--evidence-file", "source=relative.log"},
		"epoch-evidence":    {"--epoch-evidence", "unknown"},
		"epoch-below-zero":  {"--highest-epoch", "-1"},
		"blank-fence":       {"--fencing-evidence", " "},
	}
	modes := []string{"missing-complete-interval", "extra-argument", "target-digest"}
	for mode := range replace {
		modes = append(modes, mode)
	}
	for mode := range values {
		modes = append(modes, mode)
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			deps, _, _ := testDependencies()
			deps.LoadConfig = func(context.Context) (*config.Config, error) {
				t.Fatal("invalid history request loaded configuration")
				return nil, nil
			}
			deps.WriteImportedHistory = func(context.Context, *config.Config, recovery.WriteHistoryRequest) (recovery.HistoryWriteReport, error) {
				t.Fatal("invalid history request reached the writer")
				return recovery.HistoryWriteReport{}, nil
			}
			args := writeHistoryArgs(t)
			if change, ok := replace[mode]; ok {
				args[slices.Index(args, change[0])] = change[1]
			}
			if change, ok := values[mode]; ok {
				args[slices.Index(args, change[0])+1] = change[1]
			}
			switch mode {
			case "missing-complete-interval":
				args = slices.DeleteFunc(args, func(value string) bool { return value == "--complete-interval=true" })
			case "extra-argument":
				args = append(args, "unexpected")
			case "target-digest":
				args = append(args, "--expected-target-sha256", "invalid")
			}
			err := Run(t.Context(), args, deps)
			require.Error(t, err)
			require.Equal(t, ExitCodeUsage, ExitCode(err))
		})
	}
}

func TestWriteHistoryCommandPreservesEvidenceAndReportsDigests(t *testing.T) {
	for _, structured := range []bool{false, true} {
		t.Run(map[bool]string{false: "human", true: "json"}[structured], func(t *testing.T) {
			deps, out, _ := testDependencies()
			args := append(writeHistoryArgs(t), "--expected-target-sha256", strings.Repeat("c", 64), "--valkey-incarnation", "run:replica")
			calls := 0
			deps.WriteImportedHistory = func(_ context.Context, _ *config.Config, request recovery.WriteHistoryRequest) (recovery.HistoryWriteReport, error) {
				calls++
				history := request.History
				require.True(t, history.Attestation.WritersFenced && history.Attestation.AdmittedWorkAccounted && history.Attestation.CompleteInterval)
				require.Equal(t, "external-fence", history.Operation.FencingEvidence)
				require.Equal(t, "external-history", history.Attestation.Reference)
				require.Equal(t, time.Date(2026, 10, 3, 19, 0, 0, 0, time.UTC), history.Through)
				require.Equal(t, int64(9), history.HighestEpoch)
				require.Len(t, history.Evidence, 2)
				require.Equal(t, "archive, shelf 4", history.Evidence[0].Reference, "a reference keeps its commas")
				require.Equal(t, history.Evidence[1].Path, history.Evidence[1].Reference, "a reference defaults to the evidence path")
				require.Equal(t, "epoch", history.EpochEvidence)
				require.Empty(t, history.TargetSHA256, "the command derives the target binding")
				require.Nil(t, history.SQLExpected, "the command derives the SQL binding")
				require.Equal(t, strings.Repeat("c", 64), request.ExpectedTargetSHA256)
				require.Equal(t, "run:replica", request.ValkeyIncarnation)
				return recovery.HistoryWriteReport{Directory: history.Directory, HistorySHA256: strings.Repeat("d", 64), TargetSHA256: strings.Repeat("c", 64), DeclaredSteps: 2}, nil
			}
			if structured {
				args = append(args, "--json")
			}
			require.NoError(t, Run(t.Context(), args, deps))
			require.Equal(t, 1, calls)
			if structured {
				var result recovery.HistoryWriteReport
				require.NoError(t, json.Unmarshal(out.Bytes(), &result))
				require.Equal(t, strings.Repeat("d", 64), result.HistorySHA256)
				require.Equal(t, 2, result.DeclaredSteps)
			} else {
				require.Contains(t, out.String(), "History SHA-256: "+strings.Repeat("d", 64)+"\nRetain this digest outside the package.")
				require.Contains(t, out.String(), "Target SHA-256: "+strings.Repeat("c", 64))
				require.Contains(t, out.String(), "All admission remains closed")
			}
			require.NotContains(t, out.String(), "external-history")
		})
	}
}

func TestWriteHistoryCommandFailureHasNoSuccessReceipt(t *testing.T) {
	deps, out, _ := testDependencies()
	failure := errors.New("target drifted while the package was written")
	deps.WriteImportedHistory = func(context.Context, *config.Config, recovery.WriteHistoryRequest) (recovery.HistoryWriteReport, error) {
		return recovery.HistoryWriteReport{HistorySHA256: strings.Repeat("d", 64)}, failure
	}
	err := Run(t.Context(), append(writeHistoryArgs(t), "--json"), deps)
	require.ErrorIs(t, err, failure)
	require.Equal(t, ExitCodeRuntime, ExitCode(err))
	require.Empty(t, out.String())
}
