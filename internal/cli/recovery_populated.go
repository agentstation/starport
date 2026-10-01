package cli

import (
	"context"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	urfavecli "github.com/urfave/cli/v3"
)

// PopulatedRecoveryRequest is the private operator request for populated in-place adoption.
// Activation names the capture C of the fenced live deployment and the history H that binds it.
// PriorApproval is the last open witness record that the operator retained independently.
// PreparationDirectory is an existing private directory that retains the prepared record.
type PopulatedRecoveryRequest struct {
	Activation             recovery.ActivationRequest
	PriorApproval          recovery.Record
	PreparationDirectory   string
	ExpectedPreparedSHA256 string
}

// Format excludes operator evidence, the prior approval, and private source paths.
func (PopulatedRecoveryRequest) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<private populated recovery request>"))
}

// PopulatedRecoveryPreparation reports the retained prepared record digest.
type PopulatedRecoveryPreparation struct {
	PreparedSHA256 string `json:"prepared_sha256"`
	NextAction     string `json:"next_action"`
}

// PopulatedRecoveryPreparer verifies C and H and retains the native control observation without a claim.
type PopulatedRecoveryPreparer func(context.Context, *config.Config, PopulatedRecoveryRequest) (PopulatedRecoveryPreparation, error)

// PopulatedRecoveryActivator claims the populated targets and completes the shared activation phases.
type PopulatedRecoveryActivator func(context.Context, *config.Config, PopulatedRecoveryRequest) (recovery.ActivationResult, error)

// PopulatedRecoveryInspector reads completion and permission without changing recovery state.
type PopulatedRecoveryInspector func(context.Context, *config.Config, PopulatedRecoveryRequest) (recovery.ActivationResult, error)

type populatedRecoveryStep int

const (
	populatedRecoveryPrepare populatedRecoveryStep = iota
	populatedRecoveryActivate
	populatedRecoveryInspect
)

func newPopulatedRecoveryCommand(deps Dependencies, usageError usageErrorHandler) *urfavecli.Command {
	return &urfavecli.Command{
		Name: "adopt", Usage: "Adopt a fenced populated deployment in place from its capture and history",
		Description: "Populated adoption reopens a closed Valkey, PostgreSQL, and object storage deployment in place. It supports only a history without prefix steps. Keep every writer fenced through fresh gateway readiness.",
		Commands: []*urfavecli.Command{
			newPopulatedRecoveryStepCommand(deps, usageError, populatedRecoveryPrepare),
			newPopulatedRecoveryStepCommand(deps, usageError, populatedRecoveryActivate),
			newPopulatedRecoveryStepCommand(deps, usageError, populatedRecoveryInspect),
		},
	}
}

// populatedPrepareCommand names the adoption step that places no claim.
const populatedPrepareCommand = "prepare"

func newPopulatedRecoveryStepCommand(deps Dependencies, usageError usageErrorHandler, step populatedRecoveryStep) *urfavecli.Command {
	name, usage := populatedPrepareCommand, "Verify the capture and history and retain the native control observation"
	switch step {
	case populatedRecoveryActivate:
		name, usage = "activate", "Claim the populated targets and complete adoption while every writer remains fenced"
	case populatedRecoveryInspect:
		name, usage = "inspect", "Inspect sealed adoption completion and current permission"
	}
	flags := []urfavecli.Flag{&urfavecli.StringFlag{Name: "request-file", Required: true, Usage: "Absolute private JSON file with the original adoption request"}}
	if step != populatedRecoveryPrepare {
		flags = append(flags,
			&urfavecli.StringFlag{Name: "prepared-sha256", Required: true, Usage: "Independently retained prepared record digest"},
			&urfavecli.StringFlag{Name: "decision-sha256", Required: step == populatedRecoveryInspect, Usage: "Independently retained decision digest; required for inspection and sealed retries"})
	}
	flags = append(flags,
		&urfavecli.DurationFlag{Name: "timeout", Value: 15 * time.Minute, Usage: "Positive command deadline; this duration is not a recovery-time objective"},
		&urfavecli.BoolFlag{Name: flagStructuredJSON, Usage: jsonOutputUsage})
	return &urfavecli.Command{
		Name: name, Usage: usage, OnUsageError: usageError, Flags: flags,
		Description: "Use the original private JSON request and configured target stores. Prepare places no claim. Retain the prepared and decision digests independently for activation, inspection, and exact retries. Inspection changes no recovery state.",
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			if cmd.Duration("timeout") <= 0 {
				return urfavecli.Exit("recovery timeout must be positive", ExitCodeUsage)
			}
			request, err := readPopulatedRecoveryRequest(cmd.String("request-file"), cmd.String("prepared-sha256"), cmd.String("decision-sha256"), step)
			if err != nil {
				return urfavecli.Exit(err.Error(), ExitCodeUsage)
			}
			if !populatedRecoveryHandled(deps, step) {
				return runtimeFailure{cause: errors.New("populated recovery handler is required")}
			}
			ctx, cancel := context.WithTimeout(ctx, cmd.Duration("timeout"))
			defer cancel()
			cfg, err := deps.LoadConfig(ctx)
			if err != nil {
				return runtimeFailure{cause: err}
			}
			if step == populatedRecoveryPrepare {
				result, err := deps.PreparePopulatedRecovery(ctx, cfg, request)
				if err != nil {
					return runtimeFailure{cause: err}
				}
				if cmd.Bool(flagStructuredJSON) {
					return writeIndentedJSON(cmd.Writer, result)
				}
				_, err = fmt.Fprintf(cmd.Writer, "Prepared SHA-256: %s\nNext action: %s\nNo claim is placed. Keep every writer fenced.\n", result.PreparedSHA256, result.NextAction)
				return err
			}
			run := deps.ActivatePopulatedRecovery
			if step == populatedRecoveryInspect {
				run = PopulatedRecoveryActivator(deps.InspectPopulatedRecovery)
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

func populatedRecoveryHandled(deps Dependencies, step populatedRecoveryStep) bool {
	switch step {
	case populatedRecoveryPrepare:
		return deps.PreparePopulatedRecovery != nil
	case populatedRecoveryActivate:
		return deps.ActivatePopulatedRecovery != nil
	default:
		return deps.InspectPopulatedRecovery != nil
	}
}

func readPopulatedRecoveryRequest(path, prepared, decision string, step populatedRecoveryStep) (PopulatedRecoveryRequest, error) {
	var request PopulatedRecoveryRequest
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return request, errors.New("adoption request file must use a canonical absolute path")
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
		return request, errors.New("invalid adoption request JSON")
	}
	if prepared != "" {
		if request.ExpectedPreparedSHA256 != "" && request.ExpectedPreparedSHA256 != prepared {
			return request, errors.New("prepared digest differs from the adoption request")
		}
		request.ExpectedPreparedSHA256 = prepared
	}
	if decision != "" {
		if request.Activation.ExpectedDecisionSHA256 != "" && request.Activation.ExpectedDecisionSHA256 != decision {
			return request, errors.New("decision digest differs from the adoption request")
		}
		request.Activation.ExpectedDecisionSHA256 = decision
	}
	if request.Activation.Validate() != nil || !request.Activation.PreserveTargetWorkspace || request.PriorApproval == (recovery.Record{}) || !filepath.IsAbs(request.PreparationDirectory) || filepath.Clean(request.PreparationDirectory) != request.PreparationDirectory {
		return request, errors.New("adoption requires valid original recovery inputs, the prior approval, a canonical preparation directory, and explicit target workspace preservation")
	}
	switch step {
	case populatedRecoveryPrepare:
		if request.ExpectedPreparedSHA256 != "" || request.Activation.ExpectedDecisionSHA256 != "" {
			return request, errors.New("adoption preparation accepts no prepared or decision digest")
		}
	case populatedRecoveryActivate, populatedRecoveryInspect:
		if !sha256Hex(request.ExpectedPreparedSHA256) {
			return request, errors.New("adoption requires the independently retained prepared digest")
		}
		if step == populatedRecoveryInspect && request.Activation.ExpectedDecisionSHA256 == "" {
			return request, errors.New("adoption inspection requires the independently retained decision digest")
		}
	}
	return request, nil
}

func sha256Hex(s string) bool {
	decoded, err := hex.DecodeString(s)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == s
}

func writeActivationResult(w io.Writer, result recovery.ActivationResult) error {
	_, err := fmt.Fprintf(w, "Decision SHA-256: %s\nCompleted native phases: %d of 3\nHistorical completion: %t\nCurrent admission valid: %t\nRestricted: %t\nNext action: %s\nFresh gateway startup and readiness remain separate checks.\n", result.DecisionSHA256, result.CompletedPhases, result.HistoricallyComplete, result.CurrentAdmissionValid, result.Restricted, result.NextAction)
	return err
}
