package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	urfavecli "github.com/urfave/cli/v3"
)

// ImportedHistoryWriter writes a final-only history package and leaves every target store unchanged.
type ImportedHistoryWriter func(context.Context, *config.Config, recovery.WriteHistoryRequest) (recovery.HistoryWriteReport, error)

func newWriteHistoryCommand(deps Dependencies, usageError usageErrorHandler) *urfavecli.Command {
	return &urfavecli.Command{
		Name: "write-history", Usage: "Write the final-only recovery history that binds a verified backup to the fenced target", OnUsageError: usageError,
		Description: "Keep every writer fenced. Run this command after prepare or inspect-import and before apply-history or activate. Use an existing empty private history directory outside the backup, the scratch directory, and every target path. The package contains only the final KV and SQL authority rotations. The command records the digest and size of each evidence file, and it changes no target store. Remove a partial directory before a retry.",
		// Evidence references can contain commas.
		DisableSliceFlagSeparator: true,
		Flags: []urfavecli.Flag{
			&urfavecli.StringFlag{Name: flagBackupDirectory, Required: true, Usage: backupDirectoryUsage},
			&urfavecli.StringFlag{Name: flagBackupManifestSHA256, Required: true, Usage: backupManifestDigestUsage},
			&urfavecli.StringFlag{Name: flagBackupOperation, Required: true, Usage: "Unchanged restore operation ID"},
			&urfavecli.StringFlag{Name: flagBackupFencingEvidence, Required: true, Usage: "Unchanged external writer-fencing evidence reference"},
			&urfavecli.StringFlag{Name: "history-directory", Required: true, Usage: "Existing empty private directory for the new history package"},
			&urfavecli.StringFlag{Name: "expected-target-sha256", Usage: "Target digest from the closed import inspection receipt; the command refuses a different target"},
			&urfavecli.StringFlag{Name: flagBackupScratch, Usage: backupScratchUsage},
			&urfavecli.StringFlag{Name: flagValkeyIncarnation, Usage: "Recorded run_id:master_replid for Valkey; omit for Badger"},
			&urfavecli.StringFlag{Name: "mode", Required: true, Usage: "Recovery mode: planned_migration or disaster_recovery"},
			&urfavecli.StringFlag{Name: "disposition", Required: true, Usage: "History disposition: replay_complete or remain_restricted"},
			&urfavecli.StringFlag{Name: "through", Required: true, Usage: "RFC 3339 end of the covered interval; not before the backup finished"},
			&urfavecli.StringFlag{Name: "end-reference", Required: true, Usage: "Non-secret reference to the evidence that ends the interval"},
			&urfavecli.Int64Flag{Name: "highest-epoch", Required: true, Usage: "Highest recovery epoch that any source used; not below the backup epoch"},
			&urfavecli.StringFlag{Name: "epoch-reference", Required: true, Usage: "Non-secret reference to the highest-epoch record"},
			&urfavecli.StringFlag{Name: "epoch-operator", Required: true, Usage: "Operator who established the highest epoch"},
			&urfavecli.StringSliceFlag{Name: "evidence-file", Required: true, Usage: "Retained evidence as id=path or id=path=reference; repeat for each file; the path cannot contain '='"},
			&urfavecli.StringFlag{Name: "epoch-evidence", Required: true, Usage: "Evidence file ID that holds the highest-epoch record"},
			&urfavecli.StringFlag{Name: "operator", Required: true, Usage: "Operator who accepts the external recovery facts"},
			&urfavecli.StringFlag{Name: "attestation-reference", Required: true, Usage: "Non-secret reference to independent fencing and interval evidence"},
			&urfavecli.BoolFlag{Name: "writers-fenced", Required: true, Usage: "Attest that every external writer remains stopped and fenced"},
			&urfavecli.BoolFlag{Name: "admitted-work-accounted", Required: true, Usage: "Attest that the retained evidence accounts for previously admitted work"},
			&urfavecli.BoolFlag{Name: "complete-interval", Required: true, Usage: "Attest that the evidence covers the complete interval"},
			&urfavecli.BoolFlag{Name: flagStructuredJSON, Usage: jsonOutputUsage},
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			request, err := writeHistoryRequest(cmd)
			if err != nil {
				return urfavecli.Exit(err.Error(), ExitCodeUsage)
			}
			if err := request.Validate(); err != nil {
				return urfavecli.Exit(err.Error(), ExitCodeUsage)
			}
			if deps.WriteImportedHistory == nil {
				return runtimeFailure{cause: errors.New("imported history writer is required")}
			}
			cfg, err := deps.LoadConfig(ctx)
			if err != nil {
				return runtimeFailure{cause: err}
			}
			result, err := deps.WriteImportedHistory(ctx, cfg, request)
			if err != nil {
				return runtimeFailure{cause: err}
			}
			if cmd.Bool(flagStructuredJSON) {
				return writeIndentedJSON(cmd.Writer, result)
			}
			_, err = fmt.Fprintf(cmd.Writer, "Wrote %d declared recovery steps.\nHistory SHA-256: %s\nRetain this digest outside the package.\nTarget SHA-256: %s\nAll admission remains closed. Use both digests with apply-history or activate.\n", result.DeclaredSteps, result.HistorySHA256, result.TargetSHA256)
			return err
		},
	}
}

func writeHistoryRequest(cmd *urfavecli.Command) (recovery.WriteHistoryRequest, error) {
	through, err := time.Parse(time.RFC3339, cmd.String("through"))
	if err != nil {
		return recovery.WriteHistoryRequest{}, errors.New("history writing requires an RFC 3339 interval end time")
	}
	var evidence []recovery.HistoryEvidenceFile
	for _, value := range cmd.StringSlice("evidence-file") {
		id, rest, ok := strings.Cut(value, "=")
		if !ok {
			return recovery.WriteHistoryRequest{}, errors.New("history writing requires each evidence file as id=path or id=path=reference")
		}
		path, reference, ok := strings.Cut(rest, "=")
		if !ok {
			reference = path
		}
		evidence = append(evidence, recovery.HistoryEvidenceFile{ID: id, Path: path, Reference: reference})
	}
	return recovery.WriteHistoryRequest{
		VerifyRequest:        recovery.VerifyRequest{Directory: cmd.String(flagBackupDirectory), ManifestSHA256: cmd.String(flagBackupManifestSHA256), ScratchDirectory: cmd.String(flagBackupScratch)},
		ExpectedTargetSHA256: cmd.String("expected-target-sha256"), ValkeyIncarnation: cmd.String(flagValkeyIncarnation),
		History: recovery.HistoryWriteRequest{
			Directory: cmd.String("history-directory"),
			Operation: recovery.RestoreOperation{ID: cmd.String(flagBackupOperation), FencingEvidence: cmd.String(flagBackupFencingEvidence)},
			Mode:      cmd.String("mode"), Disposition: cmd.String("disposition"), Through: through.UTC(), EndReference: cmd.String("end-reference"),
			HighestEpoch: cmd.Int64("highest-epoch"), EpochReference: cmd.String("epoch-reference"), EpochOperator: cmd.String("epoch-operator"), EpochEvidence: cmd.String("epoch-evidence"),
			Evidence:    evidence,
			Attestation: recovery.HistoryAttestation{Operator: cmd.String("operator"), Reference: cmd.String("attestation-reference"), WritersFenced: cmd.Bool("writers-fenced"), AdmittedWorkAccounted: cmd.Bool("admitted-work-accounted"), CompleteInterval: cmd.Bool("complete-interval")},
		},
	}, nil
}
