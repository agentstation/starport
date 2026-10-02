package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/configrevision"
	urfavecli "github.com/urfave/cli/v3"
)

// SharedConfigurationInitializer writes, or previews, revision 1 of the shared
// deployment configuration.
type SharedConfigurationInitializer func(context.Context, *config.Config, configrevision.Request) (configrevision.Result, error)

// ConfigurationMigrator records a configuration authority switch.
type ConfigurationMigrator func(context.Context, *config.Config, string, configrevision.Request) (configrevision.Result, error)

// ConfigurationApplier commits the next shared revision and fences the fleet.
type ConfigurationApplier func(context.Context, *config.Config, configrevision.ApplyRequest) (configrevision.Result, error)

// EffectiveConfigurationReader reports each catalog setting and its authority.
type EffectiveConfigurationReader func(context.Context, *config.Config) (config.EffectiveReport, error)

const (
	flagConfigShared = "shared"
	flagConfigYes    = "yes"
	flagConfigTarget = "to"
	flagConfigResume = "resume"
)

// newConfigurationAuthorityCommands returns the commands that select and
// change the deployment configuration authority.
func newConfigurationAuthorityCommands(deps Dependencies, usageError usageErrorHandler) []*urfavecli.Command {
	operationFlag := func() urfavecli.Flag {
		return &urfavecli.StringFlag{Name: flagBackupOperation, Usage: "Stable operation ID for retry after interruption (generated when absent)"}
	}
	jsonFlag := func() urfavecli.Flag {
		return &urfavecli.BoolFlag{Name: flagStructuredJSON, Usage: jsonOutputUsage}
	}
	initialize := &urfavecli.Command{
		Name: initCommand, Usage: "Preview or write revision 1 of the shared deployment configuration",
		OnUsageError: usageError,
		Flags: []urfavecli.Flag{
			&urfavecli.BoolFlag{Name: flagConfigShared, Required: true, Usage: "Seed the shared configuration from the local deployment values"},
			&urfavecli.BoolFlag{Name: flagConfigYes, Usage: "Write the revision. Without it, the command previews"},
			operationFlag(), jsonFlag(),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			if !cmd.Bool(flagConfigShared) {
				return urfavecli.Exit("config init requires --shared", ExitCodeUsage)
			}
			if deps.InitializeSharedConfiguration == nil {
				return runtimeFailure{cause: errors.New("shared configuration initializer is required")}
			}
			cfg, err := deps.LoadConfig(ctx)
			if err != nil {
				return runtimeFailure{cause: fmt.Errorf("load configuration: %w", config.OperatorError(err))}
			}
			result, err := deps.InitializeSharedConfiguration(ctx, cfg, configrevision.Request{
				OperationID: cmd.String(flagBackupOperation), Preview: !cmd.Bool(flagConfigYes),
			})
			if err != nil {
				return runtimeFailure{cause: err}
			}
			if cmd.Bool(flagStructuredJSON) {
				return writeIndentedJSON(cmd.Writer, result)
			}
			if !result.Written {
				_, err = fmt.Fprintf(cmd.Writer, "Preview: shared configuration revision 1 in namespace %s, checksum %s.\nWrite it with: starport config init --shared --yes\n",
					result.Revision.Namespace, result.Revision.Checksum)
				return err
			}
			return writeConfigurationResult(cmd.Writer, result, "Set STARPORT_CONFIG_MANAGEMENT=shared on each gateway, then start it.")
		},
	}
	migrate := &urfavecli.Command{
		Name: "migrate", Usage: "Record a switch between local and shared configuration authority",
		OnUsageError: usageError,
		Flags: []urfavecli.Flag{
			&urfavecli.StringFlag{Name: flagConfigTarget, Required: true, Usage: "Target authority: shared or local"},
			&urfavecli.BoolFlag{Name: flagConfigYes, Usage: "Confirm the release of the shared authority (required with --to local)"},
			operationFlag(), jsonFlag(),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			target := cmd.String(flagConfigTarget)
			switch target {
			case configrevision.AuthorityShared:
			case configrevision.AuthorityLocal:
				if !cmd.Bool(flagConfigYes) {
					return urfavecli.Exit("config migrate --to local releases the shared authority. Confirm it with --yes", ExitCodeUsage)
				}
			default:
				return urfavecli.Exit(fmt.Sprintf("config migrate --to %q is not shared or local", target), ExitCodeUsage)
			}
			if deps.MigrateConfiguration == nil {
				return runtimeFailure{cause: errors.New("configuration migrator is required")}
			}
			cfg, err := deps.LoadConfig(ctx)
			if err != nil {
				return runtimeFailure{cause: fmt.Errorf("load configuration: %w", config.OperatorError(err))}
			}
			result, err := deps.MigrateConfiguration(ctx, cfg, target, configrevision.Request{OperationID: cmd.String(flagBackupOperation)})
			if err != nil {
				return runtimeFailure{cause: err}
			}
			if cmd.Bool(flagStructuredJSON) {
				return writeIndentedJSON(cmd.Writer, result)
			}
			return writeConfigurationResult(cmd.Writer, result, fmt.Sprintf("Set STARPORT_CONFIG_MANAGEMENT=%s on each gateway, then restart it.", target))
		},
	}
	apply := &urfavecli.Command{
		Name: "apply", Usage: "Commit the local deployment values as the next shared revision and fence the fleet",
		OnUsageError: usageError,
		Flags: []urfavecli.Flag{
			operationFlag(),
			&urfavecli.StringFlag{Name: flagConfigResume, Usage: "Repeat only the fleet phase of the revision that this operation ID committed"},
			jsonFlag(),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			request := configrevision.ApplyRequest{OperationID: cmd.String(flagBackupOperation)}
			if cmd.IsSet(flagConfigResume) {
				if request.OperationID != "" {
					return urfavecli.Exit("config apply --resume names the operation. Do not combine it with --operation", ExitCodeUsage)
				}
				request = configrevision.ApplyRequest{OperationID: cmd.String(flagConfigResume), Resume: true}
				if strings.TrimSpace(request.OperationID) == "" {
					return urfavecli.Exit("config apply --resume requires an operation ID", ExitCodeUsage)
				}
			}
			if deps.ApplyConfiguration == nil {
				return runtimeFailure{cause: errors.New("configuration applier is required")}
			}
			cfg, err := deps.LoadConfig(ctx)
			if err != nil {
				return runtimeFailure{cause: fmt.Errorf("load configuration: %w", config.OperatorError(err))}
			}
			result, err := deps.ApplyConfiguration(ctx, cfg, request)
			if err != nil {
				return runtimeFailure{cause: err}
			}
			if cmd.Bool(flagStructuredJSON) {
				return writeIndentedJSON(cmd.Writer, result)
			}
			return writeConfigurationResult(cmd.Writer, result, "Restart each gateway to apply it.")
		},
	}
	effective := &urfavecli.Command{
		Name: "effective", Usage: "Show each catalog setting with its authority, origin, and revision",
		OnUsageError: usageError, Flags: []urfavecli.Flag{jsonFlag()},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			if deps.EffectiveConfiguration == nil {
				return runtimeFailure{cause: errors.New("effective configuration reader is required")}
			}
			cfg, err := deps.LoadConfig(ctx)
			if err != nil {
				return runtimeFailure{cause: fmt.Errorf("load configuration: %w", config.OperatorError(err))}
			}
			report, err := deps.EffectiveConfiguration(ctx, cfg)
			if err != nil {
				return runtimeFailure{cause: err}
			}
			if cmd.Bool(flagStructuredJSON) {
				return writeIndentedJSON(cmd.Writer, report)
			}
			return writeEffectiveConfiguration(cmd.Writer, report)
		},
	}
	return []*urfavecli.Command{initialize, migrate, apply, effective}
}

func writeConfigurationResult(writer io.Writer, result configrevision.Result, next string) error {
	revision := result.Revision
	verb := "Wrote"
	if !result.Written {
		verb = "Resumed"
	}
	if _, err := fmt.Fprintf(writer, "%s %s configuration revision %d in namespace %s, checksum %s.\nOperation: %s\n",
		verb, revision.Authority, revision.Sequence, revision.Namespace, revision.Checksum, revision.OperationID); err != nil {
		return err
	}
	switch result.Fence {
	case configrevision.FenceApplied:
		if _, err := fmt.Fprintf(writer, "The fleet policy names revision %d. A replica with another revision cannot take or renew the catalog lease.\n", revision.Sequence); err != nil {
			return err
		}
	case configrevision.FenceNotShared:
		if _, err := fmt.Fprintln(writer, "The storage has no shared catalog lease, so no fleet policy was written."); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(writer, next)
	return err
}

func writeEffectiveConfiguration(writer io.Writer, report config.EffectiveReport) error {
	header := "Management: " + report.Management
	if report.Controller != "" {
		header += " (controller: " + report.Controller + ")"
	}
	if report.Namespace != "" {
		header += "\nNamespace: " + report.Namespace
	}
	if report.Management == config.ManagementShared {
		header += fmt.Sprintf("\nRevision: desired %d, applied %d, checksum %s", report.Revision.Desired, report.Revision.Applied, report.Revision.Checksum)
	}
	if _, err := fmt.Fprintln(writer, header); err != nil {
		return err
	}
	for _, setting := range report.Settings {
		if _, err := fmt.Fprintf(writer, "%s=%q authority=%s scope=%s origin=%s\n", setting.Name, setting.Value, setting.Authority, setting.Scope, setting.Origin); err != nil {
			return err
		}
		for _, ignored := range setting.Ignored {
			if _, err := fmt.Fprintf(writer, "  ignored %s: %s\n", ignored.Origin, ignored.Reason); err != nil {
				return err
			}
		}
	}
	return nil
}
