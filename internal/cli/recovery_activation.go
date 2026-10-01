package cli

import (
	"context"
	"encoding/json/v2"
	"errors"
	"path/filepath"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	urfavecli "github.com/urfave/cli/v3"
)

// RecoveryActivator completes the stopped deployment's coordinated recovery procedure.
type RecoveryActivator func(context.Context, *config.Config, recovery.ActivationRequest) (recovery.ActivationResult, error)

// RecoveryActivationInspector reads completion and permission without changing recovery state.
type RecoveryActivationInspector func(context.Context, *config.Config, recovery.ActivationRequest) (recovery.ActivationResult, error)

const activationRequestMaxBytes = 64 << 10

func newRecoveryActivationCommand(deps Dependencies, usageError usageErrorHandler, inspect bool) *urfavecli.Command {
	name, usage := "activate", "Complete coordinated recovery while every writer remains fenced"
	if inspect {
		name, usage = "activation-status", "Inspect sealed recovery completion and current permission"
	}
	return &urfavecli.Command{
		Name: name, Usage: usage, OnUsageError: usageError,
		Description: "Use the original private JSON request and configured target stores. Keep every writer fenced through fresh gateway readiness. Activation does not start a gateway or repeat provider requests. Status changes no recovery state. Retain the decision digest independently for status and exact retries.",
		Flags: []urfavecli.Flag{
			&urfavecli.StringFlag{Name: "request-file", Required: true, Usage: "Absolute private JSON file with the original activation request"},
			&urfavecli.StringFlag{Name: "decision-sha256", Usage: "Independently retained decision digest; required for status and sealed retries"},
			&urfavecli.DurationFlag{Name: "timeout", Value: 15 * time.Minute, Usage: "Positive command deadline; this duration is not a recovery-time objective"},
			&urfavecli.BoolFlag{Name: flagStructuredJSON, Usage: jsonOutputUsage},
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			if cmd.Duration("timeout") <= 0 {
				return urfavecli.Exit("recovery timeout must be positive", ExitCodeUsage)
			}
			request, err := readRecoveryActivationRequest(cmd.String("request-file"), cmd.String("decision-sha256"), inspect)
			if err != nil {
				return urfavecli.Exit(err.Error(), ExitCodeUsage)
			}
			var run RecoveryActivator
			if inspect {
				run = RecoveryActivator(deps.InspectRecoveryActivation)
			} else {
				run = deps.ActivateRecovery
			}
			if run == nil {
				return runtimeFailure{cause: errors.New("recovery activation handler is required")}
			}
			ctx, cancel := context.WithTimeout(ctx, cmd.Duration("timeout"))
			defer cancel()
			cfg, err := deps.LoadConfig(ctx)
			if err != nil {
				return runtimeFailure{cause: err}
			}
			result, err := run(ctx, cfg, request)
			if err != nil {
				return runtimeFailure{cause: err}
			}
			if cmd.Bool(flagStructuredJSON) {
				return writeIndentedJSON(cmd.Writer, result)
			}
			return writeActivationResult(cmd.Writer, result)
		},
	}
}

func readRecoveryActivationRequest(path, digest string, inspect bool) (recovery.ActivationRequest, error) {
	var request recovery.ActivationRequest
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return request, errors.New("activation request file must use a canonical absolute path")
	}
	directory, err := productfiles.ExistingDirectory(filepath.Dir(path))
	if err != nil {
		return request, err
	}
	body, err := directory.ReadFile(filepath.Base(path), activationRequestMaxBytes)
	if err != nil {
		return request, err
	}
	if json.Unmarshal(body, &request, json.RejectUnknownMembers(true)) != nil {
		return request, errors.New("invalid activation request JSON")
	}
	if digest != "" {
		if request.ExpectedDecisionSHA256 != "" && request.ExpectedDecisionSHA256 != digest {
			return request, errors.New("decision digest differs from the activation request")
		}
		request.ExpectedDecisionSHA256 = digest
	}
	if request.Validate() != nil || !request.PreserveTargetWorkspace {
		return request, errors.New("activation requires valid original recovery inputs and explicit target workspace preservation")
	}
	if inspect && request.ExpectedDecisionSHA256 == "" {
		return request, errors.New("activation status requires the independently retained decision digest")
	}
	return request, nil
}
