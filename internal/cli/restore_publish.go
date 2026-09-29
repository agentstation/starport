package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	urfavecli "github.com/urfave/cli/v3"
)

// BackupFilePublisher validates and publishes one canonical role without starting the gateway.
type BackupFilePublisher func(context.Context, *config.Config, recovery.PublishFilesRequest) (recovery.PublishFilesResult, error)

func newPublishBackupFilesCommand(deps Dependencies, usageError usageErrorHandler) *urfavecli.Command {
	return &urfavecli.Command{
		Name: "publish-files", Usage: "Publish a supported canonical file role; does not approve admission", OnUsageError: usageError,
		Flags: []urfavecli.Flag{
			&urfavecli.StringFlag{Name: flagBackupDirectory, Required: true, Usage: backupDirectoryUsage},
			&urfavecli.StringFlag{Name: flagBackupManifestSHA256, Required: true, Usage: backupManifestDigestUsage},
			&urfavecli.StringFlag{Name: flagBackupScratch, Usage: backupScratchUsage},
			&urfavecli.StringFlag{Name: "files-directory", Required: true, Usage: "Inactive directory from the matching preparation operation"},
			&urfavecli.StringFlag{Name: flagBackupOperation, Required: true, Usage: "Original restore operation ID"},
			&urfavecli.StringFlag{Name: flagBackupFencingEvidence, Required: true, Usage: "Original evidence that all source and target writers remain fenced"},
			&urfavecli.StringFlag{Name: "role", Required: true, Usage: "Canonical role: baseline, runtime-evidence, inference-credential-policy, or credential-policy"},
			&urfavecli.BoolFlag{Name: flagStructuredJSON, Usage: jsonOutputUsage},
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			request := recovery.PublishFilesRequest{PrepareRequest: recovery.PrepareRequest{
				VerifyRequest:  recovery.VerifyRequest{Directory: cmd.String(flagBackupDirectory), ManifestSHA256: cmd.String(flagBackupManifestSHA256), ScratchDirectory: cmd.String(flagBackupScratch)},
				FilesDirectory: cmd.String("files-directory"),
				Operation:      recovery.RestoreOperation{ID: cmd.String(flagBackupOperation), FencingEvidence: cmd.String(flagBackupFencingEvidence)},
			}, Role: cmd.String("role")}
			if err := request.Validate(); err != nil {
				return urfavecli.Exit(err.Error(), ExitCodeUsage)
			}
			if request.Role != config.InferenceCredentialPolicyRole && request.Role != config.AcquisitionPolicyRole && request.Role != config.BaselineRole && request.Role != config.RuntimeEvidenceRole {
				return urfavecli.Exit("unsupported file role for publication", ExitCodeUsage)
			}
			if deps.PublishBackupFiles == nil {
				return runtimeFailure{cause: errors.New("backup file publisher is required")}
			}
			cfg, err := deps.LoadConfig(ctx)
			if err != nil {
				return runtimeFailure{cause: err}
			}
			result, err := deps.PublishBackupFiles(ctx, cfg, request)
			if err != nil {
				return runtimeFailure{cause: err}
			}
			if cmd.Bool(flagStructuredJSON) {
				return writeIndentedJSON(cmd.Writer, result)
			}
			_, err = fmt.Fprintf(cmd.Writer, "Verified canonical role: %s\nDestination: %s\nRemaining file dispositions: %d\nAll import barriers remain. This result does not approve admission.\n", result.Role, result.Tree.Destination, len(result.Remaining))
			return err
		},
	}
}
