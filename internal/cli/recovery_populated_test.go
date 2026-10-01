package cli

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
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

func populatedCommandFixture(t *testing.T) (string, PopulatedRecoveryRequest) {
	t.Helper()
	_, activation := activationCommandFixture(t)
	request := PopulatedRecoveryRequest{
		Activation:           activation,
		PriorApproval:        recovery.Record{DeploymentID: "deployment-a", BackendID: "valkey-a", Epoch: 7, Open: true, Evidence: "prior-approval"},
		PreparationDirectory: t.TempDir(),
	}
	parent := filepath.Join(t.TempDir(), "private-request")
	_, err := productfiles.CreateDirectory(parent)
	require.NoError(t, err)
	file := filepath.Join(parent, "adoption.json")
	writePopulatedCommandRequest(t, file, request)
	return file, request
}

func writePopulatedCommandRequest(t *testing.T, file string, request PopulatedRecoveryRequest) {
	t.Helper()
	body, err := json.Marshal(request)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(file, body, 0600))
}

func requireBoundedDeadline(t *testing.T, ctx context.Context) {
	t.Helper()
	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	require.Greater(t, time.Until(deadline), time.Duration(0))
	require.LessOrEqual(t, time.Until(deadline), time.Second)
}

func refusePopulatedHandlers(t *testing.T, deps *Dependencies) {
	deps.PreparePopulatedRecovery = func(context.Context, *config.Config, PopulatedRecoveryRequest) (PopulatedRecoveryPreparation, error) {
		t.Fatal("unexpected adoption preparation")
		return PopulatedRecoveryPreparation{}, nil
	}
	deps.ActivatePopulatedRecovery = func(context.Context, *config.Config, PopulatedRecoveryRequest) (recovery.ActivationResult, error) {
		t.Fatal("unexpected adoption activation")
		return recovery.ActivationResult{}, nil
	}
	deps.InspectPopulatedRecovery = func(context.Context, *config.Config, PopulatedRecoveryRequest) (recovery.ActivationResult, error) {
		t.Fatal("unexpected adoption inspection")
		return recovery.ActivationResult{}, nil
	}
}

func TestPopulatedRecoveryCommandsSelectOneBoundedHandler(t *testing.T) {
	prepared, decision := strings.Repeat("1", 64), strings.Repeat("d", 64)
	for _, name := range []string{"prepare", "activate", "inspect"} {
		t.Run(name, func(t *testing.T) {
			file, request := populatedCommandFixture(t)
			deps, output, _ := testDependencies()
			refusePopulatedHandlers(t, &deps)
			calls := 0
			args := []string{"starport", "backup", "adopt", name, "--request-file", file, "--timeout", "1s", "--json"}
			run := func(ctx context.Context, _ *config.Config, actual PopulatedRecoveryRequest) (recovery.ActivationResult, error) {
				calls++
				requireBoundedDeadline(t, ctx)
				require.Equal(t, request, actual)
				return recovery.ActivationResult{DecisionSHA256: decision, CompletedPhases: 3, HistoricallyComplete: true, Restricted: true, NextAction: "inspect current catalog permission and deployment approval"}, nil
			}
			switch name {
			case "prepare":
				deps.PreparePopulatedRecovery = func(ctx context.Context, _ *config.Config, actual PopulatedRecoveryRequest) (PopulatedRecoveryPreparation, error) {
					calls++
					requireBoundedDeadline(t, ctx)
					require.Equal(t, request, actual)
					return PopulatedRecoveryPreparation{PreparedSHA256: prepared, NextAction: "activate"}, nil
				}
			case "activate":
				request.ExpectedPreparedSHA256 = prepared
				args = append(args, "--prepared-sha256", prepared)
				deps.ActivatePopulatedRecovery = run
			case "inspect":
				request.ExpectedPreparedSHA256 = prepared
				request.Activation.ExpectedDecisionSHA256 = decision
				args = append(args, "--prepared-sha256", prepared, "--decision-sha256", decision)
				deps.InspectPopulatedRecovery = run
			}
			require.NoError(t, Run(t.Context(), args, deps))
			require.Equal(t, 1, calls)
			if name == "prepare" {
				var result PopulatedRecoveryPreparation
				require.NoError(t, json.Unmarshal(output.Bytes(), &result))
				require.Equal(t, prepared, result.PreparedSHA256)
				return
			}
			var result recovery.ActivationResult
			require.NoError(t, json.Unmarshal(output.Bytes(), &result))
			require.True(t, result.HistoricallyComplete)
			require.True(t, result.Restricted)
			require.False(t, result.CurrentAdmissionValid)
		})
	}
}

func TestPopulatedRecoveryRejectsInputBeforeLoadingConfiguration(t *testing.T) {
	prepared := strings.Repeat("1", 64)
	for _, mode := range []string{"relative", "missing", "directory", "malformed", "unknown-member", "oversize", "no-preservation", "no-prior-approval", "relative-preparation", "incomplete-interval", "prepare-with-digest", "activate-without-prepared", "activate-malformed-prepared", "inspect-without-decision", "prepared-conflict", "decision-conflict", "zero-timeout", "negative-timeout", "extra-argument"} {
		t.Run(mode, func(t *testing.T) {
			file, request := populatedCommandFixture(t)
			name, timeout := "activate", "1s"
			args := []string{"--prepared-sha256", prepared}
			switch mode {
			case "relative":
				file = "adoption.json"
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
				request.Activation.PreserveTargetWorkspace = false
				writePopulatedCommandRequest(t, file, request)
			case "no-prior-approval":
				request.PriorApproval = recovery.Record{}
				writePopulatedCommandRequest(t, file, request)
			case "relative-preparation":
				request.PreparationDirectory = "preparation"
				writePopulatedCommandRequest(t, file, request)
			case "incomplete-interval":
				request.Activation.History.Attestation.CompleteInterval = false
				writePopulatedCommandRequest(t, file, request)
			case "prepare-with-digest":
				name = "prepare"
				args = nil
				request.ExpectedPreparedSHA256 = prepared
				writePopulatedCommandRequest(t, file, request)
			case "activate-without-prepared":
				args = []string{"--prepared-sha256", ""}
			case "activate-malformed-prepared":
				args = []string{"--prepared-sha256", strings.ToUpper(prepared[:62]) + "zz"}
			case "inspect-without-decision":
				name = "inspect"
			case "prepared-conflict":
				request.ExpectedPreparedSHA256 = strings.Repeat("2", 64)
				writePopulatedCommandRequest(t, file, request)
			case "decision-conflict":
				request.Activation.ExpectedDecisionSHA256 = strings.Repeat("e", 64)
				writePopulatedCommandRequest(t, file, request)
				args = append(args, "--decision-sha256", strings.Repeat("d", 64))
			case "zero-timeout":
				timeout = "0s"
			case "negative-timeout":
				timeout = "-1s"
			}
			deps, output, _ := testDependencies()
			refusePopulatedHandlers(t, &deps)
			deps.LoadConfig = func(context.Context) (*config.Config, error) {
				t.Fatal("invalid adoption loaded configuration")
				return nil, nil
			}
			command := append([]string{"starport", "backup", "adopt", name, "--request-file", file, "--timeout", timeout}, args...)
			if mode == "extra-argument" {
				command = append(command, "extra")
			}
			err := Run(t.Context(), command, deps)
			require.Error(t, err)
			require.Equal(t, ExitCodeUsage, ExitCode(err))
			require.NotContains(t, output.String(), "Historical completion: true")
			require.NotContains(t, output.String(), "Prepared SHA-256")
		})
	}
}

func TestPopulatedRecoveryFailurePrintsNoCompletion(t *testing.T) {
	prepared := strings.Repeat("1", 64)
	failure := errors.New("interrupted populated claim")
	for _, name := range []string{"prepare", "activate", "inspect"} {
		t.Run(name, func(t *testing.T) {
			file, _ := populatedCommandFixture(t)
			deps, output, _ := testDependencies()
			refusePopulatedHandlers(t, &deps)
			args := []string{"starport", "backup", "adopt", name, "--request-file", file}
			fail := func(context.Context, *config.Config, PopulatedRecoveryRequest) (recovery.ActivationResult, error) {
				return recovery.ActivationResult{DecisionSHA256: "must-not-appear", HistoricallyComplete: true}, failure
			}
			switch name {
			case "prepare":
				deps.PreparePopulatedRecovery = func(context.Context, *config.Config, PopulatedRecoveryRequest) (PopulatedRecoveryPreparation, error) {
					return PopulatedRecoveryPreparation{PreparedSHA256: "must-not-appear"}, failure
				}
			case "activate":
				args = append(args, "--prepared-sha256", prepared)
				deps.ActivatePopulatedRecovery = fail
			case "inspect":
				args = append(args, "--prepared-sha256", prepared, "--decision-sha256", strings.Repeat("d", 64))
				deps.InspectPopulatedRecovery = fail
			}
			err := Run(t.Context(), args, deps)
			require.ErrorIs(t, err, failure)
			require.Equal(t, ExitCodeRuntime, ExitCode(err))
			require.Empty(t, output.String())
		})
	}
}

func TestPopulatedRecoveryRequestFormatExcludesPrivateInputs(t *testing.T) {
	_, request := populatedCommandFixture(t)
	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		formatted := fmt.Sprintf(format, request)
		require.Equal(t, "<private populated recovery request>", formatted)
	}
}

func TestPopulatedRecoveryHelpExplainsFencingAndFreshReadiness(t *testing.T) {
	deps, output, _ := testDependencies()
	require.NoError(t, Run(t.Context(), []string{"starport", "backup", "adopt", "--help"}, deps))
	require.Contains(t, output.String(), "without prefix steps")
	require.Contains(t, output.String(), "every writer fenced through fresh gateway readiness")
	for _, name := range []string{"prepare", "activate", "inspect"} {
		deps, output, _ := testDependencies()
		require.NoError(t, Run(t.Context(), []string{"starport", "backup", "adopt", name, "--help"}, deps))
		require.Contains(t, output.String(), "--request-file")
		require.Contains(t, output.String(), "Prepare places no claim")
		if name == "prepare" {
			require.NotContains(t, output.String(), "--prepared-sha256")
			continue
		}
		require.Contains(t, output.String(), "--prepared-sha256")
		require.Contains(t, output.String(), "--decision-sha256")
	}
}
