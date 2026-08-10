package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/diagnosis"
	urfavecli "github.com/urfave/cli/v3"
)

func newConfigCommand(deps Dependencies, usageError usageErrorHandler) *urfavecli.Command {
	jsonFlag := func() urfavecli.Flag {
		return &urfavecli.BoolFlag{
			Name:  configFormatJSON,
			Usage: "Write machine-readable JSON",
		}
	}
	show := &urfavecli.Command{
		Name: "show", Usage: "Show effective configuration with secrets redacted",
		OnUsageError: usageError, Flags: []urfavecli.Flag{jsonFlag()},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			cfg, err := deps.LoadConfig(ctx)
			if err != nil {
				return runtimeFailure{cause: fmt.Errorf("load configuration: %w", config.OperatorError(err))}
			}
			if err := writeConfiguration(cmd.Writer, cfg, cmd.Bool(configFormatJSON)); err != nil {
				return runtimeFailure{cause: fmt.Errorf("write configuration: %w", err)}
			}
			return nil
		},
	}
	paths := &urfavecli.Command{
		Name: "paths", Usage: "Show platform configuration and data paths",
		OnUsageError: usageError, Flags: []urfavecli.Flag{jsonFlag()},
		Action: func(_ context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			resolved, err := deps.ResolvePaths()
			if err != nil {
				return runtimeFailure{cause: errors.New("resolve configuration paths: platform paths could not be resolved")}
			}
			if err := writePaths(cmd.Writer, resolved, cmd.Bool(configFormatJSON)); err != nil {
				return runtimeFailure{cause: fmt.Errorf("write configuration paths: %w", err)}
			}
			return nil
		},
	}
	validate := &urfavecli.Command{
		Name: "validate", Usage: "Validate effective configuration",
		OnUsageError: usageError, Flags: []urfavecli.Flag{jsonFlag()},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			if _, err := deps.LoadConfig(ctx); err != nil {
				operatorErr := config.OperatorError(err)
				if cmd.Bool(configFormatJSON) {
					if writeErr := writeValidation(cmd.Writer, true, operatorErr); writeErr != nil {
						return runtimeFailure{cause: fmt.Errorf("write validation result: %w", writeErr)}
					}
				}
				return runtimeFailure{cause: fmt.Errorf("configuration is invalid: %w", operatorErr)}
			}
			if err := writeValidation(cmd.Writer, cmd.Bool(configFormatJSON), nil); err != nil {
				return runtimeFailure{cause: fmt.Errorf("write validation result: %w", err)}
			}
			return nil
		},
	}
	return &urfavecli.Command{
		Name: "config", Usage: "Inspect and validate effective configuration",
		OnUsageError: usageError, Commands: []*urfavecli.Command{show, paths, validate},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if cmd.NArg() == 0 {
				return urfavecli.ShowSubcommandHelp(cmd)
			}
			return usageError(
				ctx,
				cmd,
				fmt.Errorf("unknown config command %q", cmd.Args().First()),
				true,
			)
		},
	}
}

func newDoctorCommand(deps Dependencies, usageError usageErrorHandler) *urfavecli.Command {
	return &urfavecli.Command{
		Name: "doctor", Usage: "Run read-only startup checks",
		OnUsageError: usageError,
		Flags: []urfavecli.Flag{
			&urfavecli.BoolFlag{
				Name:  "probe",
				Usage: "Open configured storage in read-only mode",
			},
			&urfavecli.BoolFlag{
				Name:  doctorFormatJSON,
				Usage: "Write machine-readable JSON",
			},
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			report := deps.Diagnose(ctx, diagnosis.Options{Probe: cmd.Bool("probe")})
			if err := writeDiagnosis(cmd.Writer, report, cmd.Bool(doctorFormatJSON)); err != nil {
				return runtimeFailure{cause: fmt.Errorf("write diagnosis: %w", err)}
			}
			if !report.OK {
				return runtimeFailure{cause: ErrDiagnosisFailed}
			}
			return nil
		},
	}
}
