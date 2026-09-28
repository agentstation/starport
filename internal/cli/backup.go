package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	urfavecli "github.com/urfave/cli/v3"
)

// BackupCloser closes recovery approval without starting a gateway.
type BackupCloser func(context.Context, *config.Config) (recovery.Record, error)

// BackupCapturer captures the stopped deployment selected by configuration.
type BackupCapturer func(context.Context, *config.Config, recovery.CaptureRequest) (recovery.CaptureResult, error)

// BackupVerifier verifies a bundle without opening live stores.
type BackupVerifier func(context.Context, *config.Config, recovery.VerifyRequest) (recovery.CaptureResult, error)

func newBackupCommand(deps Dependencies, usageError usageErrorHandler) *urfavecli.Command {
	return &urfavecli.Command{Name: "backup", Usage: "Capture and verify a stopped deployment", Commands: []*urfavecli.Command{
		{
			Name: "close", Usage: "Close recovery approval; separately stop and fence all writers", OnUsageError: usageError,
			Flags: []urfavecli.Flag{&urfavecli.BoolFlag{Name: flagStructuredJSON, Usage: jsonOutputUsage}},
			Action: func(ctx context.Context, cmd *urfavecli.Command) error {
				if err := rejectArguments(cmd); err != nil {
					return err
				}
				if deps.CloseBackupBoundary == nil {
					return runtimeFailure{cause: errors.New("backup boundary closer is required")}
				}
				cfg, err := deps.LoadConfig(ctx)
				if err != nil {
					return runtimeFailure{cause: err}
				}
				record, err := deps.CloseBackupBoundary(ctx, cfg)
				if err != nil {
					return runtimeFailure{cause: err}
				}
				if cmd.Bool(flagStructuredJSON) {
					return writeIndentedJSON(cmd.Writer, record)
				}
				_, err = fmt.Fprintf(cmd.Writer, "Recovery approval is closed for %s at epoch %d.\nStop and fence all writers before capture. This command does not stop processes.\n", record.DeploymentID, record.Epoch)
				return err
			},
		},
		{
			Name: "create", Usage: "Capture existing stores and files after every writer is stopped and fenced", OnUsageError: usageError,
			Flags: []urfavecli.Flag{
				&urfavecli.StringFlag{Name: "destination", Required: true, Usage: "New absolute directory under an existing private parent"},
				&urfavecli.StringFlag{Name: flagBackupOperation, Required: true, Usage: "Backup operation ID"},
				&urfavecli.StringFlag{Name: "fencing-evidence", Required: true, Usage: "Non-secret reference to proof that all writers are stopped and fenced"},
				&urfavecli.StringFlag{Name: "key-reference", Required: true, Usage: "Recovery reference for the configured master key; never the key value"},
				&urfavecli.IntFlag{Name: "entry-limit", Usage: "Maximum local file inspection entries; zero selects the default"},
				&urfavecli.BoolFlag{Name: flagStructuredJSON, Usage: jsonOutputUsage},
			},
			Action: func(ctx context.Context, cmd *urfavecli.Command) error {
				if err := rejectArguments(cmd); err != nil {
					return err
				}
				request := recovery.CaptureRequest{Destination: cmd.String("destination"), OperationID: cmd.String(flagBackupOperation), Build: deps.Build.Version, FencingEvidence: cmd.String("fencing-evidence"), KeyReference: cmd.String("key-reference"), EntryLimit: cmd.Int("entry-limit")}
				if err := request.Validate(); err != nil {
					return urfavecli.Exit(err.Error(), ExitCodeUsage)
				}
				if deps.CaptureBackup == nil {
					return runtimeFailure{cause: errors.New("backup capturer is required")}
				}
				cfg, err := deps.LoadConfig(ctx)
				if err != nil {
					return runtimeFailure{cause: err}
				}
				result, err := deps.CaptureBackup(ctx, cfg, request)
				if err != nil {
					return runtimeFailure{cause: err}
				}
				return writeBackupResult(cmd, result)
			},
		},
		{
			Name: "verify", Usage: "Check backup bytes, credentials, and references; does not approve recovery", OnUsageError: usageError,
			Flags: []urfavecli.Flag{
				&urfavecli.StringFlag{Name: "directory", Required: true, Usage: "Absolute backup directory"},
				&urfavecli.StringFlag{Name: "scratch", Usage: "Existing private directory for verification copies; defaults to the backup parent"},
				&urfavecli.StringFlag{Name: "manifest-sha256", Required: true, Usage: "Manifest digest retained independently of the backup"},
				&urfavecli.BoolFlag{Name: flagStructuredJSON, Usage: jsonOutputUsage},
			},
			Action: func(ctx context.Context, cmd *urfavecli.Command) error {
				if err := rejectArguments(cmd); err != nil {
					return err
				}
				request := recovery.VerifyRequest{Directory: cmd.String("directory"), ManifestSHA256: cmd.String("manifest-sha256"), ScratchDirectory: cmd.String("scratch")}
				if err := request.Validate(); err != nil {
					return urfavecli.Exit(err.Error(), ExitCodeUsage)
				}
				if deps.VerifyBackup == nil {
					return runtimeFailure{cause: errors.New("backup verifier is required")}
				}
				cfg, err := deps.LoadConfig(ctx)
				if err != nil {
					return runtimeFailure{cause: err}
				}
				result, err := deps.VerifyBackup(ctx, cfg, request)
				if err != nil {
					return runtimeFailure{cause: err}
				}
				return writeBackupResult(cmd, result)
			},
		},
	}}
}

func writeBackupResult(cmd *urfavecli.Command, result recovery.CaptureResult) error {
	if cmd.Bool(flagStructuredJSON) {
		return writeIndentedJSON(cmd.Writer, result)
	}
	_, err := fmt.Fprintf(cmd.Writer, "Verified %d artifacts for %s at recovery epoch %d.\nDirectory: %s\nManifest SHA-256: %s\nRetain this digest outside the backup. Verification does not approve recovery.\n", result.Artifacts, result.DeploymentID, result.RecoveryEpoch, result.Directory, result.ManifestSHA256)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.Writer, "Reference checks: %d credential values, %d file records, %d jobs, %d batches.\nUnconfirmed provider submissions: %d; unfinished batch lines: %d; missing batch file references: %d.\nRetain unresolved work for reconciliation. Verification does not authorize retries.\n", result.References.CredentialValues, result.References.FileRecords, result.References.JobRecords, result.References.BatchRecords, result.References.UncertainJobs, result.References.UnfinishedBatchLines, result.References.MissingBatchFiles)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.Writer, "Identity checks: %d accounts, %d templates, %d users, %d teams, %d memberships, %d grants.\nGrants referencing missing accounts: %d.\n", result.References.AccountRecords, result.References.AccountTemplates, result.References.Identity.Users, result.References.Identity.Teams, result.References.Identity.Memberships, result.References.Identity.Grants, result.References.Identity.MissingGrantAccounts)
	return err
}
