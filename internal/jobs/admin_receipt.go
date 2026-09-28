package jobs

import (
	"context"
	"errors"
	"reflect"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/storage"
)

// retainAdministratorReceipt preserves independent evidence before deleting its blob.
// A later response cannot replace the accepted administrator decision.
func (s *Service) retainAdministratorReceipt(ctx context.Context, job Job) (Job, error) {
	receipt, _, err := s.readNativeReceipt(ctx, job)
	if errors.Is(err, blob.ErrNotFound) {
		return job, nil
	}
	if err != nil {
		return job, err
	}
	evidence := &LateProviderEvidence{RequestID: receipt.RequestID, State: receipt.State, RecordedAt: receipt.RecordedAt, Measurement: copyMeasurement(receipt.Measurement), AssetDigest: receipt.AssetDigest}
	for range 8 {
		if job.lateProviderEvidence != nil {
			if !reflect.DeepEqual(job.lateProviderEvidence, evidence) {
				return job, ErrReconciliationConflict
			}
			// Retain the private response until its original asset deadline.
			if s.now().Before(receipt.RecordedAt.Add(job.nativeRetention)) {
				return job, nil
			}
			// Billing evidence survives private URL and asset expiry.
			if err := s.assets.Retire(ctx, job.nativeReceiptKey); err != nil {
				return job, err
			}
			return job, nil
		}
		next := job
		next.lateProviderEvidence = evidence
		if err := next.Validate(); err != nil {
			return job, err
		}
		if next.BillingConflict() {
			if err := s.recordBillingConflict(ctx, next); err != nil {
				return job, err
			}
		}
		err := s.records.Replace(ctx, job, next)
		if err == nil {
			job = next
			continue
		}
		if !errors.Is(err, storage.ErrConflict) {
			return job, err
		}
		job, err = s.records.Get(ctx, job.Account, job.ID)
		if err != nil {
			return Job{}, err
		}
	}
	return job, storage.ErrConflict
}
