package proxy

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/usage"
)

// ErrCorrectionReportingUnavailable retains a pending adjustment when its reporter cannot apply it.
var ErrCorrectionReportingUnavailable = errors.New("proxy: usage correction reporting is unavailable")

// RecordJobCorrection retains the original report before applying a billing adjustment.
// It uses the valuation and evidence retained by the job, without provider traffic.
func (a *JobAccountant) RecordJobCorrection(ctx context.Context, correction jobs.AccountingCorrection) error {
	if a == nil || a.recorder == nil {
		return nil
	}
	writer, ok := a.recorder.(usage.AdjustmentWriter)
	if !ok {
		return ErrCorrectionReportingUnavailable
	}
	evidence := correction.Evidence
	amount := int64(0)
	if evidence.NoCharge {
		if len(evidence.Quantities) != 0 || evidence.Tokens != 0 {
			return usage.ErrInvalidRecord
		}
	} else {
		if correction.Original.Valuation == nil {
			return usage.ErrInvalidRecord
		}
		var err error
		amount, err = correction.Original.Valuation.NanoUSD(evidence.Quantities)
		if err != nil {
			return err
		}
	}
	disposition := "administrator_usage"
	if evidence.NoCharge {
		disposition = "administrator_no_charge"
	}
	adjustment := usage.Adjustment{
		ID: correction.ID, PreviousID: correction.PreviousID,
		Original: jobUsageRecord(correction.Original), RecordedAt: correction.RecordedAt,
		Cost: usage.Cost{NanoUSD: amount, Currency: "USD"}, Tokens: evidence.Tokens, BillingDisposition: disposition,
	}
	if err := adjustment.Validate(); err != nil {
		return err
	}
	adjustment.ID = reportingCorrectionID(correction.Original, correction.ID)
	if correction.PreviousID != "" {
		adjustment.PreviousID = reportingCorrectionID(correction.Original, correction.PreviousID)
	}
	if err := a.put(ctx, adjustment.Original); err != nil {
		return err
	}
	return writer.Adjust(ctx, adjustment)
}

// reportingCorrectionID keeps private decision identifiers out of activity results.
func reportingCorrectionID(original jobs.AccountingEntry, id string) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%q:%q:%q", original.Account, original.JobID, id)))
	return fmt.Sprintf("%x", digest)
}
