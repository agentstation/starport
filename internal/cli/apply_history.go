package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	urfavecli "github.com/urfave/cli/v3"
)

// ImportedHistoryApplier accepts independent evidence and leaves admission closed.
type ImportedHistoryApplier func(context.Context, *config.Config, recovery.ApplyHistoryRequest) (recovery.HistoryReplayReport, error)

func newApplyHistoryCommand(deps Dependencies, usageError usageErrorHandler) *urfavecli.Command {
	return &urfavecli.Command{
		Name: "apply-history", Usage: "Accept independent recovery evidence and replay owner transitions with all admission closed", OnUsageError: usageError,
		Description: "Keep every writer fenced. Use the target digest from inspect-import and the unchanged restore operation. Retain the same private journal for exact retries after interruption. External attestations assert facts that Starport cannot establish. This command neither activates stores nor repeats provider requests.",
		Flags: []urfavecli.Flag{
			&urfavecli.StringFlag{Name: flagBackupDirectory, Required: true, Usage: backupDirectoryUsage},
			&urfavecli.StringFlag{Name: flagBackupManifestSHA256, Required: true, Usage: backupManifestDigestUsage},
			&urfavecli.StringFlag{Name: flagBackupOperation, Required: true, Usage: "Unchanged restore operation ID"},
			&urfavecli.StringFlag{Name: flagBackupFencingEvidence, Required: true, Usage: "Unchanged external writer-fencing evidence reference"},
			&urfavecli.StringFlag{Name: "history-directory", Required: true, Usage: "Existing private directory with the independent history manifest, payloads, and assets"},
			&urfavecli.StringFlag{Name: "history-sha256", Required: true, Usage: "History manifest digest retained independently of its files"},
			&urfavecli.StringFlag{Name: "expected-target-sha256", Required: true, Usage: "Target digest from the closed import inspection receipt"},
			&urfavecli.StringFlag{Name: "journal-directory", Required: true, Usage: "Existing private journal directory retained across every exact retry"},
			&urfavecli.StringFlag{Name: flagBackupScratch, Usage: backupScratchUsage},
			&urfavecli.StringFlag{Name: "valkey-incarnation", Usage: "Recorded run_id:master_replid for Valkey; omit for Badger"},
			&urfavecli.StringFlag{Name: "operator", Required: true, Usage: "Operator who accepts the external recovery facts"},
			&urfavecli.StringFlag{Name: "attestation-reference", Required: true, Usage: "Non-secret reference to independent fencing and interval evidence"},
			&urfavecli.BoolFlag{Name: "writers-fenced", Required: true, Usage: "Attest that every external writer remains stopped and fenced"},
			&urfavecli.BoolFlag{Name: "admitted-work-accounted", Required: true, Usage: "Attest that the retained evidence accounts for previously admitted work"},
			&urfavecli.BoolFlag{Name: "complete-interval", Usage: "Accept complete interval coverage; required for a replay_complete disposition"},
			&urfavecli.BoolFlag{Name: flagStructuredJSON, Usage: jsonOutputUsage},
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			request := recovery.ApplyHistoryRequest{
				VerifyRequest:    recovery.VerifyRequest{Directory: cmd.String(flagBackupDirectory), ManifestSHA256: cmd.String(flagBackupManifestSHA256), ScratchDirectory: cmd.String(flagBackupScratch)},
				Operation:        recovery.RestoreOperation{ID: cmd.String(flagBackupOperation), FencingEvidence: cmd.String(flagBackupFencingEvidence)},
				HistoryDirectory: cmd.String("history-directory"), HistorySHA256: cmd.String("history-sha256"), ExpectedTargetSHA256: cmd.String("expected-target-sha256"),
				JournalDirectory: cmd.String("journal-directory"), ValkeyIncarnation: cmd.String("valkey-incarnation"),
				Attestation: recovery.HistoryAttestation{Operator: cmd.String("operator"), Reference: cmd.String("attestation-reference"), WritersFenced: cmd.Bool("writers-fenced"), AdmittedWorkAccounted: cmd.Bool("admitted-work-accounted"), CompleteInterval: cmd.Bool("complete-interval")},
			}
			if err := request.Validate(); err != nil {
				return urfavecli.Exit(err.Error(), ExitCodeUsage)
			}
			if deps.ApplyImportedHistory == nil {
				return runtimeFailure{cause: errors.New("imported history applier is required")}
			}
			cfg, err := deps.LoadConfig(ctx)
			if err != nil {
				return runtimeFailure{cause: err}
			}
			result, err := deps.ApplyImportedHistory(ctx, cfg, request)
			if err != nil {
				return runtimeFailure{cause: err}
			}
			if cmd.Bool(flagStructuredJSON) {
				return writeIndentedJSON(cmd.Writer, result)
			}
			_, err = fmt.Fprintf(cmd.Writer, "Applied %d of %d declared recovery steps.\nAcceptance SHA-256: %s\nJournal SHA-256: %s\nAll admission remains closed. Retain the journal for exact retries and final recovery checks.\n", result.CompletedSteps, result.DeclaredSteps, result.AcceptanceSHA256, result.JournalSHA256)
			return err
		},
	}
}
