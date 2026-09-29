package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	urfavecli "github.com/urfave/cli/v3"
)

// BackupPreparer imports a verified backup into configured isolated targets.
type BackupPreparer func(context.Context, *config.Config, recovery.PrepareRequest) (recovery.PrepareResult, error)

func newPrepareBackupCommand(deps Dependencies, usageError usageErrorHandler) *urfavecli.Command {
	return &urfavecli.Command{
		Name: "prepare", Usage: "Restore into isolated configured stores; does not approve admission", OnUsageError: usageError,
		Flags: []urfavecli.Flag{
			&urfavecli.StringFlag{Name: flagBackupDirectory, Required: true, Usage: backupDirectoryUsage},
			&urfavecli.StringFlag{Name: flagBackupManifestSHA256, Required: true, Usage: backupManifestDigestUsage},
			&urfavecli.StringFlag{Name: flagBackupScratch, Usage: "Existing private scratch directory; defaults to the backup parent"},
			&urfavecli.StringFlag{Name: "files-directory", Required: true, Usage: "Inactive directory for selected files and the preparation receipt"},
			&urfavecli.StringFlag{Name: flagBackupOperation, Required: true, Usage: "Restore operation ID; retain it for exact retries"},
			&urfavecli.StringFlag{Name: flagBackupFencingEvidence, Required: true, Usage: "Non-secret reference to proof that all source and target writers are fenced"},
			&urfavecli.BoolFlag{Name: flagStructuredJSON, Usage: jsonOutputUsage},
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			request := recovery.PrepareRequest{
				VerifyRequest:  recovery.VerifyRequest{Directory: cmd.String(flagBackupDirectory), ManifestSHA256: cmd.String(flagBackupManifestSHA256), ScratchDirectory: cmd.String(flagBackupScratch)},
				FilesDirectory: cmd.String("files-directory"),
				Operation:      recovery.RestoreOperation{ID: cmd.String(flagBackupOperation), FencingEvidence: cmd.String(flagBackupFencingEvidence)},
			}
			if err := request.Validate(); err != nil {
				return urfavecli.Exit(err.Error(), ExitCodeUsage)
			}
			if deps.PrepareBackup == nil {
				return runtimeFailure{cause: errors.New("backup preparer is required")}
			}
			cfg, err := deps.LoadConfig(ctx)
			if err != nil {
				return runtimeFailure{cause: err}
			}
			result, err := deps.PrepareBackup(ctx, cfg, request)
			if err != nil {
				return runtimeFailure{cause: err}
			}
			if cmd.Bool(flagStructuredJSON) {
				return writeIndentedJSON(cmd.Writer, result)
			}
			_, err = fmt.Fprintf(cmd.Writer, "Prepared restricted recovery for %s at epoch %d.\nOperation: %s\nManifest SHA-256: %s\nInactive files: %s\nFile recovery: %d selected files await their required publication procedures.\nUse --json to inspect target locations and file recovery actions.\nAll import barriers remain. This result does not approve admission.\nComplete independent reconciliation and activation before starting the gateway.\n", result.Prepared.Boundary.DeploymentID, result.Prepared.Boundary.Epoch, result.Prepared.OperationID, result.Prepared.ManifestSHA256, result.FilesDirectory, len(result.FilePlan))
			return err
		},
	}
}
