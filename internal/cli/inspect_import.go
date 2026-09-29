package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	urfavecli "github.com/urfave/cli/v3"
)

// ImportedBackupInspector checks closed imported targets without approving activation.
type ImportedBackupInspector func(context.Context, *config.Config, recovery.InspectImportRequest) (recovery.ImportInspectionResult, error)

func newInspectImportedBackupCommand(deps Dependencies, usageError usageErrorHandler) *urfavecli.Command {
	return &urfavecli.Command{
		Name: "inspect-import", Usage: "Check a closed imported graph and retain private inspection evidence; does not approve activation", OnUsageError: usageError,
		Description: "Use the source manifest digest and unchanged operation/fencing reference from preparation. Supply each replay sequence and receipt digest from the last accepted replay receipt, or explicitly set its sequence to 0 for prepared state. Supply the exact current closed recovery boundary from preparation or accepted epoch evidence. Keep all writers fenced. Each inspection needs a new private output directory.",
		Flags: []urfavecli.Flag{
			&urfavecli.StringFlag{Name: flagBackupDirectory, Required: true, Usage: backupDirectoryUsage},
			&urfavecli.StringFlag{Name: flagBackupManifestSHA256, Required: true, Usage: backupManifestDigestUsage},
			&urfavecli.StringFlag{Name: flagBackupOperation, Required: true, Usage: "Unchanged restore operation ID from preparation"},
			&urfavecli.StringFlag{Name: flagBackupFencingEvidence, Required: true, Usage: "Unchanged external writer-fencing evidence reference from preparation"},
			&urfavecli.StringFlag{Name: "destination", Required: true, Usage: "New inspection directory under an existing private parent"},
			&urfavecli.StringFlag{Name: flagBackupScratch, Usage: backupScratchUsage},
			&urfavecli.StringFlag{Name: "expected-deployment", Required: true, Usage: "Deployment ID from the expected closed recovery boundary"},
			&urfavecli.Int64Flag{Name: "expected-recovery-epoch", Required: true, Usage: "Epoch from the exact expected closed recovery boundary"},
			&urfavecli.StringFlag{Name: "expected-recovery-evidence", Required: true, Usage: "Evidence reference from the exact expected closed recovery boundary"},
			&urfavecli.StringFlag{Name: "expected-recovery-backend", Usage: "Backend ID from the expected closed boundary; omit only when empty"},
			&urfavecli.Int64Flag{Name: "kv-replay-sequence", Required: true, Usage: "Last accepted native KV replay sequence; explicitly use 0 before replay"},
			&urfavecli.StringFlag{Name: "kv-replay-sha256", Usage: "KV receipt digest; required when its sequence is positive"},
			&urfavecli.Int64Flag{Name: "sql-replay-sequence", Required: true, Usage: "Last accepted SQL replay sequence; explicitly use 0 before replay"},
			&urfavecli.StringFlag{Name: "sql-replay-sha256", Usage: "SQL receipt digest; required when its sequence is positive"},
			&urfavecli.StringFlag{Name: "valkey-incarnation", Usage: "Recorded run_id:master_replid identity for configured Valkey; omit for Badger"},
			&urfavecli.BoolFlag{Name: flagStructuredJSON, Usage: jsonOutputUsage},
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			request := recovery.InspectImportRequest{
				VerifyRequest:     recovery.VerifyRequest{Directory: cmd.String(flagBackupDirectory), ManifestSHA256: cmd.String(flagBackupManifestSHA256), ScratchDirectory: cmd.String(flagBackupScratch)},
				Operation:         recovery.RestoreOperation{ID: cmd.String(flagBackupOperation), FencingEvidence: cmd.String(flagBackupFencingEvidence)},
				ExpectedBoundary:  recovery.Record{DeploymentID: cmd.String("expected-deployment"), Epoch: cmd.Int64("expected-recovery-epoch"), Evidence: cmd.String("expected-recovery-evidence"), BackendID: cmd.String("expected-recovery-backend")},
				KVPosition:        storage.ImportReplayPosition{Sequence: cmd.Int64("kv-replay-sequence"), ReceiptSHA256: cmd.String("kv-replay-sha256")},
				SQLPosition:       sqlstore.RelationalReplayPosition{Sequence: cmd.Int64("sql-replay-sequence"), ReceiptSHA256: cmd.String("sql-replay-sha256")},
				ValkeyIncarnation: cmd.String("valkey-incarnation"), Destination: cmd.String("destination"),
			}
			if err := request.Validate(); err != nil {
				return urfavecli.Exit(err.Error(), ExitCodeUsage)
			}
			if deps.InspectImportedBackup == nil {
				return runtimeFailure{cause: errors.New("imported backup inspector is required")}
			}
			cfg, err := deps.LoadConfig(ctx)
			if err != nil {
				return runtimeFailure{cause: err}
			}
			result, err := deps.InspectImportedBackup(ctx, cfg, request)
			if err != nil {
				return runtimeFailure{cause: err}
			}
			if cmd.Bool(flagStructuredJSON) {
				return writeIndentedJSON(cmd.Writer, result)
			}
			_, err = fmt.Fprintf(cmd.Writer, "Inspected closed import for %s at recovery epoch %d.\nEvidence: %s\nInspection SHA-256: %s\nAll barriers remain. Catalog and reference checks do not approve activation or provider retries.\n", request.ExpectedBoundary.DeploymentID, request.ExpectedBoundary.Epoch, request.Destination, result.Inspection.RequestSHA256)
			return err
		},
	}
}
