package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	urfavecli "github.com/urfave/cli/v3"
)

const (
	flagBackupDirectory       = "directory"
	flagBackupManifestSHA256  = "manifest-sha256"
	flagBackupFencingEvidence = "fencing-evidence"
	flagBackupScratch         = "scratch"
	backupDirectoryUsage      = "Absolute backup directory"
	backupManifestDigestUsage = "Manifest digest retained independently of the backup"
)

// BackupCloser closes recovery approval without starting a gateway.
type BackupCloser func(context.Context, *config.Config) (recovery.Record, error)

// BackupCapturer captures the stopped deployment selected by configuration.
type BackupCapturer func(context.Context, *config.Config, recovery.CaptureRequest) (recovery.CaptureResult, error)

// BackupVerifier verifies a bundle without opening live stores.
type BackupVerifier func(context.Context, *config.Config, recovery.VerifyRequest) (recovery.CaptureResult, error)

func newBackupCommand(deps Dependencies, usageError usageErrorHandler) *urfavecli.Command {
	return &urfavecli.Command{Name: "backup", Usage: "Capture and verify a stopped deployment", Commands: []*urfavecli.Command{
		newPrepareBackupCommand(deps, usageError),
		newInspectImportedBackupCommand(deps, usageError),
		newPublishBackupFilesCommand(deps, usageError),
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
				&urfavecli.StringFlag{Name: flagBackupFencingEvidence, Required: true, Usage: "Non-secret reference to proof that all writers are stopped and fenced"},
				&urfavecli.StringFlag{Name: "key-reference", Required: true, Usage: "Recovery reference for the configured master key; never the key value"},
				&urfavecli.IntFlag{Name: "entry-limit", Usage: "Maximum local file inspection entries; zero selects the default"},
				&urfavecli.BoolFlag{Name: "unprefixed-valkey", Usage: "Capture a dedicated unprefixed Valkey database for namespace migration; all source writers must be fenced"},
				&urfavecli.BoolFlag{Name: flagStructuredJSON, Usage: jsonOutputUsage},
			},
			Action: func(ctx context.Context, cmd *urfavecli.Command) error {
				if err := rejectArguments(cmd); err != nil {
					return err
				}
				request := recovery.CaptureRequest{Destination: cmd.String("destination"), OperationID: cmd.String(flagBackupOperation), Build: deps.Build.Version, FencingEvidence: cmd.String(flagBackupFencingEvidence), KeyReference: cmd.String("key-reference"), EntryLimit: cmd.Int("entry-limit"), UnprefixedValkey: cmd.Bool("unprefixed-valkey")}
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
				&urfavecli.StringFlag{Name: flagBackupDirectory, Required: true, Usage: backupDirectoryUsage},
				&urfavecli.StringFlag{Name: flagBackupScratch, Usage: "Existing private directory for verification copies; defaults to the backup parent"},
				&urfavecli.StringFlag{Name: flagBackupManifestSHA256, Required: true, Usage: backupManifestDigestUsage},
				&urfavecli.BoolFlag{Name: flagStructuredJSON, Usage: jsonOutputUsage},
			},
			Action: func(ctx context.Context, cmd *urfavecli.Command) error {
				if err := rejectArguments(cmd); err != nil {
					return err
				}
				request := recovery.VerifyRequest{Directory: cmd.String(flagBackupDirectory), ManifestSHA256: cmd.String(flagBackupManifestSHA256), ScratchDirectory: cmd.String(flagBackupScratch)}
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
	if err != nil {
		return err
	}
	keys := result.References.GatewayKeys
	if _, err := fmt.Fprintf(cmd.Writer, "Captured KV records: %d. Unprefixed Valkey source: %t.\n", result.KVRecords, result.UnprefixedValkey); err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.Writer, "Gateway keys: %d; hash indexes: %d; missing accounts: %d; missing teams: %d; deleted initial keys: %d.\nBudget records: %d; verified windows: %d; held reservations: %d; retained team origins: %d.\nUnknown budget histories: %d account, %d key, %d team.\nUnknown history does not establish zero consumption or permission.\n", keys.Keys, keys.HashIndexes, keys.MissingAccounts, keys.MissingTeams, keys.MissingInitialKeys, result.References.BudgetRecords, result.References.BudgetWindows, result.References.HeldReservations, result.References.Identity.BudgetOrigins, result.References.UnknownAccountBudgetHistories, keys.UnknownBudgetHistories, result.References.Identity.UnknownBudgetHistories)
	if err != nil {
		return err
	}
	r := result.References
	_, err = fmt.Fprintf(cmd.Writer, "Jobs with reservations: %d. Pending settlement: %d. Reservations with missing jobs: %d.\nJob correction intents: %d. Applied: %d. Reported: %d.\nMissing jobs do not release reservations or authorize provider retries.\n", r.JobExecution.ReservedJobs, r.JobExecution.PendingSettlement, r.MissingReservationJobs, r.JobCorrections.Intents, r.JobCorrections.Applied, r.JobCorrections.Reported)

	return err
}

const backupScratchUsage = "Existing private scratch directory; defaults to the backup parent"
