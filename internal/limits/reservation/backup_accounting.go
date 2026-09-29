package reservation

import (
	"context"
	"math"

	"github.com/agentstation/starport/internal/limits"
)

// BackupTotals contains contributions from retained attempts, excluding the window seed.
type BackupTotals struct {
	Consumed int64
	Reserved int64
	Disputes int64
	Overflow bool
}

// BackupContribution binds one attempt's accounting to its original window.
type BackupContribution struct {
	WindowKey string
	Totals    BackupTotals
}

// AddBackupTotals uses the same consumption saturation as live settlement.
// Reservation and dispute overflow indicate inconsistent retained state.
func AddBackupTotals(left, right BackupTotals) (BackupTotals, error) {
	if left.Consumed < 0 || left.Reserved < 0 || left.Disputes < 0 || right.Consumed < 0 || right.Reserved < 0 || right.Disputes < 0 || right.Reserved > math.MaxInt64-left.Reserved || right.Disputes > math.MaxInt64-left.Disputes {
		return BackupTotals{}, ErrUnavailable
	}
	result := BackupTotals{Reserved: left.Reserved + right.Reserved, Disputes: left.Disputes + right.Disputes, Overflow: left.Overflow || right.Overflow}
	if right.Consumed > math.MaxInt64-left.Consumed {
		result.Consumed, result.Overflow = math.MaxInt64, true
	} else {
		result.Consumed = left.Consumed + right.Consumed
	}
	return result, nil
}

// VerifyBackupTotals compares the complete retained attempt totals with a captured window.
// It does not prove that the backup includes later acknowledged work.
func VerifyBackupTotals(window WindowState, totals BackupTotals) error {
	expected, err := AddBackupTotals(totals, BackupTotals{Consumed: window.SeedConsumed})
	if err != nil {
		return err
	}
	if expected.Consumed != window.Consumed || expected.Reserved != window.Reserved || expected.Disputes != window.ActiveDisputes || expected.Overflow != window.Overflow {
		return ErrUnavailable
	}
	return nil
}

func backupContributions(record *Record) ([]BackupContribution, error) {
	result := make([]BackupContribution, 0, len(record.Bindings))
	var actual int64
	if record.State == Settled {
		var err error
		actual, err = record.Attempt.evidenceAmount(record.Evidence)
		if err != nil {
			return nil, err
		}
	}
	for _, binding := range record.Bindings {
		total := BackupTotals{Overflow: record.Unresolved != nil}
		if record.DisputeID != "" {
			total.Disputes = 1
		}
		switch record.State {
		case Reserved, Dispatched, Uncertain:
			total.Reserved = binding.Amount
		case Settled:
			total.Consumed = actual
			if binding.Rule.Meter.Dimension == limits.DimensionTokens {
				total.Consumed = record.Evidence.Tokens
			}
		}
		result = append(result, BackupContribution{WindowKey: meterKey(binding.Rule.Meter, binding.Window), Totals: total})
	}
	return result, nil
}

// verifyBackupCorrectionChain checks every edge and detects cycles with constant memory.
func verifyBackupCorrectionChain(ctx context.Context, source BackupReader, record Record) (count int64, err error) {
	anchor, span, power := record.CorrectionID, int64(0), int64(1)
	for record.CorrectionID != "" {
		if err := ctx.Err(); err != nil {
			return count, err
		}
		data, err := readBackupValue(ctx, source, correctionKey(record.Attempt.ID, record.CorrectionID))
		if err != nil {
			return count, err
		}
		receipt, err := decodeCorrectionReceipt(record.Attempt.ID, record.CorrectionID, data)
		if err != nil {
			return count, err
		}
		if _, err := correctionWindow(receipt.Before, receipt.RecordedAt); err != nil {
			return count, err
		}
		if err := verifyCorrectionRecord(record, *receipt); err != nil {
			return count, err
		}
		if count == math.MaxInt64 {
			return count, ErrUnavailable
		}
		count++
		record = receipt.Before
		span++
		if record.CorrectionID != "" && record.CorrectionID == anchor {
			return count, ErrUnavailable
		}
		if span == power {
			anchor, span = record.CorrectionID, 0
			if power > math.MaxInt64/2 {
				return count, ErrUnavailable
			}
			power *= 2
		}
	}
	return count, nil
}
