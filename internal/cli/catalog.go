package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	urfavecli "github.com/urfave/cli/v3"
)

const (
	flagPromotionOperation = "operation-id"
	flagExpectedRevision   = "expected-revision"
)

// ErrBaselinePromotionRefused reports a promotion receipt with the refused status.
var ErrBaselinePromotionRefused = errors.New("baseline promotion refused")

// BaselinePromoter makes the packaged baseline of this binary the retained fleet baseline.
type BaselinePromoter func(context.Context, *config.Config, runtimecatalog.PromotionRequest) (runtimecatalog.PromotionReceipt, error)

// BaselineStatusReader compares the packaged baseline of this binary with the retained fleet baseline.
type BaselineStatusReader func(context.Context, *config.Config) (runtimecatalog.BaselineReport, error)

func newCatalogCommand(deps Dependencies, usageError usageErrorHandler) *urfavecli.Command {
	return &urfavecli.Command{Name: "catalog", Usage: "Manage the retained fleet catalog baseline", Commands: []*urfavecli.Command{{
		Name: "promote-baseline", Usage: "Make the packaged baseline of this binary the retained fleet baseline", OnUsageError: usageError,
		Flags: []urfavecli.Flag{
			&urfavecli.StringFlag{Name: flagPromotionOperation, Required: true, Usage: "Stable promotion ID for retry after interruption"},
			&urfavecli.Uint64Flag{Name: flagExpectedRevision, Usage: "Fleet head revision from baseline-status. Zero accepts the current head"},
			&urfavecli.BoolFlag{Name: flagStructuredJSON, Usage: jsonOutputUsage},
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			request := runtimecatalog.PromotionRequest{OperationID: cmd.String(flagPromotionOperation), ExpectedRevision: cmd.Uint64(flagExpectedRevision)}
			if err := runtimecatalog.ValidatePromotionOperationID(request.OperationID); err != nil {
				return urfavecli.Exit(err.Error(), ExitCodeUsage)
			}
			if deps.PromoteCatalogBaseline == nil {
				return runtimeFailure{cause: errors.New("baseline promoter is required")}
			}
			cfg, err := deps.LoadConfig(ctx)
			if err != nil {
				return runtimeFailure{cause: err}
			}
			receipt, err := deps.PromoteCatalogBaseline(ctx, cfg, request)
			if err != nil {
				return runtimeFailure{cause: err}
			}
			if cmd.Bool(flagStructuredJSON) {
				err = writeIndentedJSON(cmd.Writer, receipt)
			} else {
				err = writePromotionReceipt(cmd.Writer, receipt)
			}
			if err != nil {
				return err
			}
			if receipt.Status != runtimecatalog.PromotionApplied {
				return runtimeFailure{cause: ErrBaselinePromotionRefused}
			}
			return nil
		},
	}, {
		Name: "baseline-status", Usage: "Compare the packaged baseline of this binary with the retained fleet baseline", OnUsageError: usageError,
		Flags: []urfavecli.Flag{&urfavecli.BoolFlag{Name: flagStructuredJSON, Usage: jsonOutputUsage}},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if err := rejectArguments(cmd); err != nil {
				return err
			}
			if deps.CatalogBaselineStatus == nil {
				return runtimeFailure{cause: errors.New("baseline status reader is required")}
			}
			cfg, err := deps.LoadConfig(ctx)
			if err != nil {
				return runtimeFailure{cause: err}
			}
			report, err := deps.CatalogBaselineStatus(ctx, cfg)
			if err != nil {
				return runtimeFailure{cause: err}
			}
			if cmd.Bool(flagStructuredJSON) {
				return writeIndentedJSON(cmd.Writer, report)
			}
			return writeBaselineReport(cmd.Writer, report)
		},
	}}}
}

func writePromotionReceipt(w io.Writer, receipt runtimecatalog.PromotionReceipt) error {
	_, err := fmt.Fprintf(w, "Baseline promotion %s %s for deployment %s.\nPrevious: generation %s, checksum %s, revision %d\nPromoted: generation %s, checksum %s, revision %d\nInert removals: %d\nActor: %s at %s\n",
		receipt.OperationID, receipt.Status, receipt.DeploymentID,
		receipt.Previous.GenerationID, receipt.Previous.Checksum, receipt.Previous.Revision,
		receipt.Promoted.GenerationID, receipt.Promoted.Checksum, receipt.Promoted.Revision,
		len(receipt.InertRemovals), receipt.Actor, receipt.CreatedAt.UTC().Format(time.RFC3339))
	if err == nil && receipt.Refusal != "" {
		_, err = fmt.Fprintf(w, "Refusal: %s\n", receipt.Refusal)
	}
	return err
}

func writeBaselineReport(w io.Writer, report runtimecatalog.BaselineReport) error {
	_, err := fmt.Fprintf(w, "Deployment %s at fleet head revision %d.\nPackaged: generation %s, checksum %s\nRetained: generation %s, checksum %s\nPromotable: %t\n",
		report.DeploymentID, report.HeadRevision,
		report.Packaged.GenerationID, report.Packaged.Checksum,
		report.Retained.GenerationID, report.Retained.Checksum, report.Promotable)
	if err == nil && report.Refusal != "" {
		_, err = fmt.Fprintf(w, "Refusal: %s\n", report.Refusal)
	}
	return err
}
