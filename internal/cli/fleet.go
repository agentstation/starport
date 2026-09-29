package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	urfavecli "github.com/urfave/cli/v3"
)

// FleetInitializer approves an explicitly selected fresh shared deployment.
type FleetInitializer func(context.Context, *config.Config, recovery.FreshRequest) (recovery.Record, error)

func newFleetCommand(deps Dependencies, usageError usageErrorHandler) *urfavecli.Command {
	return &urfavecli.Command{Name: "fleet", Usage: "Manage shared deployment storage", Commands: []*urfavecli.Command{{
		Name: "init", Usage: "Approve fresh Valkey and PostgreSQL stores with all gateways stopped", OnUsageError: usageError,
		Flags: []urfavecli.Flag{
			&urfavecli.StringFlag{Name: flagBackupOperation, Required: true, Usage: "Stable initialization ID for retry after interruption"},
			&urfavecli.StringFlag{Name: "evidence", Required: true, Usage: "Non-secret deployment procedure or audit reference"},
			&urfavecli.BoolFlag{Name: flagStructuredJSON, Usage: jsonOutputUsage},
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			request := recovery.FreshRequest{OperationID: cmd.String(flagBackupOperation), Evidence: cmd.String("evidence")}
			if err := request.Validate(); err != nil {
				return urfavecli.Exit(err.Error(), ExitCodeUsage)
			}
			if deps.InitializeFleet == nil {
				return runtimeFailure{cause: errors.New("fleet initializer is required")}
			}
			cfg, err := deps.LoadConfig(ctx)
			if err != nil {
				return runtimeFailure{cause: err}
			}
			record, err := deps.InitializeFleet(ctx, cfg, request)
			if err != nil {
				return runtimeFailure{cause: err}
			}
			if cmd.Bool(flagStructuredJSON) {
				return writeIndentedJSON(cmd.Writer, record)
			}
			_, err = fmt.Fprintf(cmd.Writer, "Approved fresh fleet %s at recovery epoch %d.\nCreate the first gateway key with: starport init --configured-storage\n", record.DeploymentID, record.Epoch)
			return err
		},
	}}}
}
