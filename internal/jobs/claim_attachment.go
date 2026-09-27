package jobs

import (
	"context"
	"errors"

	"github.com/agentstation/starport/internal/storage"
)

var (
	// ErrClaimAttachmentRequired refuses a claimed job without an atomic attachment.
	ErrClaimAttachmentRequired = errors.New("jobs: claimed record requires atomic attachment")
	// ErrClaimUnavailable refuses publication after the claim changes.
	ErrClaimUnavailable = errors.New("jobs: slot claim does not permit publication")
)

func (r *repository) CreateClaimed(ctx context.Context, job Job, claim storage.CompareAndSwapMutation) error {
	if job.SlotID == "" {
		return ErrClaimAttachmentRequired
	}
	data, err := encodeJob(job)
	if err != nil {
		return err
	}
	return createAttached(ctx, r.store, storageKey(job.Account, job.ID), data, claim, ErrJobExists)
}

func (r *batchRepository) CreateClaimed(ctx context.Context, batch Batch, claim storage.CompareAndSwapMutation) error {
	if batch.SlotID == "" {
		return ErrClaimAttachmentRequired
	}
	data, err := encodeBatch(batch)
	if err != nil {
		return err
	}
	return createAttached(ctx, r.store, batchStorageKey(batch.Account, batch.ID), data, claim, ErrBatchExists)
}

func createAttached(ctx context.Context, store storage.KVStore, key string, data []byte, claim storage.CompareAndSwapMutation, exists error) error {
	if claim.Key == "" || claim.Key == key || claim.NewValue == nil || claim.TTL != 0 {
		return ErrClaimAttachmentRequired
	}
	err := store.CompareAndSwapBatch(ctx, []storage.CompareAndSwapMutation{
		{Key: key, NewValue: data}, claim,
	})
	if errors.Is(err, storage.ErrConflict) {
		present, readErr := store.Exists(ctx, key)
		if readErr == nil && present {
			return exists
		}
		return errors.Join(ErrClaimUnavailable, err)
	}
	return err
}
