package cli

import (
	"context"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/stretchr/testify/require"
)

func activationCommandFixture(t *testing.T) (string, recovery.ActivationRequest) {
	t.Helper()
	verify := recovery.VerifyRequest{Directory: t.TempDir(), ManifestSHA256: strings.Repeat("a", 64)}
	operation := recovery.RestoreOperation{ID: "restore-original", FencingEvidence: "independent-writer-fence"}
	request := recovery.ActivationRequest{
		Prepare:             recovery.PrepareRequest{VerifyRequest: verify, Operation: operation, FilesDirectory: t.TempDir()},
		History:             recovery.ApplyHistoryRequest{VerifyRequest: verify, Operation: operation, HistoryDirectory: t.TempDir(), HistorySHA256: strings.Repeat("b", 64), ExpectedTargetSHA256: strings.Repeat("c", 64), JournalDirectory: t.TempDir(), Attestation: recovery.HistoryAttestation{Operator: "operator", Reference: "independent-interval", WritersFenced: true, AdmittedWorkAccounted: true, CompleteInterval: true}},
		ActivationDirectory: t.TempDir(), PreserveTargetWorkspace: true,
	}
	require.NoError(t, request.Validate())
	parent := filepath.Join(t.TempDir(), "private-request")
	_, err := productfiles.CreateDirectory(parent)
	require.NoError(t, err)
	file := filepath.Join(parent, "activation.json")
	writeActivationCommandRequest(t, file, request)
	return file, request
}

func writeActivationCommandRequest(t *testing.T, file string, request recovery.ActivationRequest) {
	t.Helper()
	body, err := json.Marshal(request)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(file, body, 0600))
}

func TestRecoveryActivationCommandsSelectOneBoundedHandler(t *testing.T) {
	for _, name := range []string{"activate", "activation-status"} {
		t.Run(name, func(t *testing.T) {
			file, request := activationCommandFixture(t)
			request.ExpectedDecisionSHA256 = strings.Repeat("d", 64)
			deps, output, _ := testDependencies()
			calls := 0
			run := func(ctx context.Context, _ *config.Config, actual recovery.ActivationRequest) (recovery.ActivationResult, error) {
				calls++
				require.Equal(t, request, actual)
				deadline, ok := ctx.Deadline()
				require.True(t, ok)
				require.Greater(t, time.Until(deadline), time.Duration(0))
				require.LessOrEqual(t, time.Until(deadline), time.Second)
				return recovery.ActivationResult{DecisionSHA256: actual.ExpectedDecisionSHA256, CompletedPhases: 3, HistoricallyComplete: true, Restricted: true, NextAction: "inspect current catalog permission and deployment approval"}, nil
			}
			if name == "activate" {
				deps.ActivateRecovery = run
				deps.InspectRecoveryActivation = func(context.Context, *config.Config, recovery.ActivationRequest) (recovery.ActivationResult, error) {
					t.Fatal("activation invoked status")
					return recovery.ActivationResult{}, nil
				}
			} else {
				deps.InspectRecoveryActivation = run
				deps.ActivateRecovery = func(context.Context, *config.Config, recovery.ActivationRequest) (recovery.ActivationResult, error) {
					t.Fatal("status invoked activation")
					return recovery.ActivationResult{}, nil
				}
			}
			require.NoError(t, Run(t.Context(), []string{"starport", "backup", name, "--request-file", file, "--decision-sha256", request.ExpectedDecisionSHA256, "--timeout", "1s", "--json"}, deps))
			require.Equal(t, 1, calls)
			var result recovery.ActivationResult
			require.NoError(t, json.Unmarshal(output.Bytes(), &result))
			require.True(t, result.HistoricallyComplete)
			require.True(t, result.Restricted)
			require.False(t, result.CurrentAdmissionValid)
		})
	}
}

func TestRecoveryActivationRejectsInputBeforeLoadingConfiguration(t *testing.T) {
	for _, mode := range []string{"relative", "missing", "directory", "malformed", "unknown-member", "oversize", "no-preservation", "different-operation", "incomplete-interval", "digest-conflict", "status-without-digest", "zero-timeout", "negative-timeout", "extra-argument"} {
		t.Run(mode, func(t *testing.T) {
			file, request := activationCommandFixture(t)
			name, timeout := "activate", "1s"
			args := []string{"--decision-sha256", strings.Repeat("d", 64)}
			switch mode {
			case "relative":
				file = "activation.json"
			case "missing":
				file = filepath.Join(t.TempDir(), "missing.json")
			case "directory":
				file = t.TempDir()
			case "malformed":
				require.NoError(t, os.WriteFile(file, []byte("{"), 0600))
			case "unknown-member":
				body, err := os.ReadFile(file)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(file, append([]byte(`{"unknown":true,`), body[1:]...), 0600))
			case "oversize":
				require.NoError(t, os.WriteFile(file, []byte(strings.Repeat(" ", activationRequestMaxBytes+1)), 0600))
			case "no-preservation":
				request.PreserveTargetWorkspace = false
				writeActivationCommandRequest(t, file, request)
			case "different-operation":
				request.History.Operation.ID = "different"
				writeActivationCommandRequest(t, file, request)
			case "incomplete-interval":
				request.History.Attestation.CompleteInterval = false
				writeActivationCommandRequest(t, file, request)
			case "digest-conflict":
				request.ExpectedDecisionSHA256 = strings.Repeat("e", 64)
				writeActivationCommandRequest(t, file, request)
			case "status-without-digest":
				name = "activation-status"
				args = nil
			case "zero-timeout":
				timeout = "0s"
			case "negative-timeout":
				timeout = "-1s"
			}
			deps, output, _ := testDependencies()
			deps.LoadConfig = func(context.Context) (*config.Config, error) {
				t.Fatal("invalid activation loaded configuration")
				return nil, nil
			}
			command := append([]string{"starport", "backup", name, "--request-file", file, "--timeout", timeout}, args...)
			if mode == "extra-argument" {
				command = append(command, "extra")
			}
			err := Run(t.Context(), command, deps)
			require.Error(t, err)
			require.Equal(t, ExitCodeUsage, ExitCode(err))
			require.NotContains(t, output.String(), "Historical completion: true")
		})
	}
}

func TestRecoveryActivationFailurePrintsNoCompletion(t *testing.T) {
	file, _ := activationCommandFixture(t)
	deps, output, _ := testDependencies()
	failure := errors.New("interrupted native release")
	deps.ActivateRecovery = func(context.Context, *config.Config, recovery.ActivationRequest) (recovery.ActivationResult, error) {
		return recovery.ActivationResult{DecisionSHA256: "must-not-appear", HistoricallyComplete: true}, failure
	}
	err := Run(t.Context(), []string{"starport", "backup", "activate", "--request-file", file, "--json"}, deps)
	require.ErrorIs(t, err, failure)
	require.Equal(t, ExitCodeRuntime, ExitCode(err))
	require.Empty(t, output.String())
}

func TestRecoveryActivationHelpExplainsFencingAndFreshReadiness(t *testing.T) {
	for _, name := range []string{"activate", "activation-status"} {
		deps, output, _ := testDependencies()
		require.NoError(t, Run(t.Context(), []string{"starport", "backup", name, "--help"}, deps))
		require.Contains(t, output.String(), "every writer fenced")
		require.Contains(t, output.String(), "fresh gateway readiness")
		require.Contains(t, output.String(), "--request-file")
		require.Contains(t, output.String(), "--decision-sha256")
	}
}
