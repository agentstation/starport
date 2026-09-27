package jobslots

import (
	"context"
	"encoding/json/v2"

	"github.com/agentstation/starport/internal/storage"
)

// Attachment prepares a pending claim for atomic publication with its job.
// The job repository must commit this mutation and its record together.
// A released or attached claim cannot authorize another publication.
func (s *Store) Attachment(ctx context.Context, account, id, jobID, kind string) (storage.CompareAndSwapMutation, error) {
	held, data, err := s.readClaim(ctx, account, id)
	if err != nil {
		return storage.CompareAndSwapMutation{}, err
	}
	if data == nil {
		return storage.CompareAndSwapMutation{}, ErrClaimNotFound
	}
	if held.Released {
		return storage.CompareAndSwapMutation{}, ErrClaimReleased
	}
	if held.Attached || held.JobID != jobID || held.Kind != kind {
		return storage.CompareAndSwapMutation{}, ErrClaimConflict
	}
	held.Attached = true
	next, err := json.Marshal(held)
	if err != nil {
		return storage.CompareAndSwapMutation{}, err
	}
	return storage.CompareAndSwapMutation{Key: claimKey(account, id), ExpectedValue: data, NewValue: next}, nil
}
