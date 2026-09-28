package jobslots

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/storage"
)

// PendingGrace allows preparation before recovery fences an unattached claim.
// Clock error can interrupt preparation, but cannot release an attached job.
const PendingGrace = 10 * time.Minute

// RecoveryResult reports examined claims and recovered capacity.
type RecoveryResult struct{ Scanned, Released, Failed int }

// RecoverPending closes old unattached claims through conditional writes.
// Attachment and recovery compete on the same claim preimage. A recovered
// claim cannot later attach to a job, even if its submitting process resumes.
func (s *Store) RecoverPending(ctx context.Context) (RecoveryResult, error) {
	if !s.recoveryMu.TryLock() {
		return RecoveryResult{}, storage.ErrConflict
	}
	defer s.recoveryMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cutoff := s.now().UTC().Add(-PendingGrace)
	var result RecoveryResult
	var first error
	for {
		if err := ctx.Err(); err != nil {
			return result, errors.Join(first, err)
		}
		if !s.recoveryLoaded {
			page, err := s.store.ScanPage(ctx, claimPrefix, s.recoveryCursor, 256)
			if err != nil {
				return result, errors.Join(first, err)
			}
			s.recoveryPending, s.recoveryNext, s.recoveryLoaded = page.Keys, page.Next, true
		}
		for len(s.recoveryPending) > 0 {
			if err := ctx.Err(); err != nil {
				return result, errors.Join(first, err)
			}
			key := s.recoveryPending[0]
			if !strings.HasSuffix(key, ":history") {
				result.Scanned++
				released, err := s.recoverKey(ctx, key, cutoff)
				if err != nil {
					result.Failed++
					if first == nil {
						first = fmt.Errorf("job slots: recover %q: %w", key, err)
					}
				}
				if released {
					result.Released++
				}
			}
			s.recoveryPending[0] = ""
			s.recoveryPending = s.recoveryPending[1:]
		}
		s.recoveryCursor, s.recoveryLoaded, s.recoveryPending = s.recoveryNext, false, nil
		if s.recoveryCursor == "" {
			return result, first
		}
	}
}

func (s *Store) recoverKey(ctx context.Context, key string, cutoff time.Time) (bool, error) {
	data, err := s.store.GetBounded(ctx, key, maxRecordBytes)
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var c Claim
	if err := json.Unmarshal(data, &c); err != nil || !c.valid() || claimKey(c.Account, c.ID) != key {
		return false, ErrInvalid
	}
	for range maxAttempts {
		if c.Attached || c.Released || c.CreatedAt.After(cutoff) {
			return false, nil
		}
		count, oldCount, err := s.readCounter(ctx, c.Account)
		if err != nil {
			return false, err
		}
		if oldCount == nil || count.Total <= 0 {
			_, current, err := s.readClaim(ctx, c.Account, c.ID)
			if err != nil {
				return false, err
			}
			if bytes.Equal(current, data) {
				return false, ErrHistoryUnknown
			}
		} else {
			closed := c
			closed.Released = true
			count.Total--
			err = s.write(ctx, closed, data, count, oldCount)
			if err == nil {
				return true, nil
			}
			if !errors.Is(err, storage.ErrConflict) {
				return false, err
			}
		}
		c, data, err = s.readClaim(ctx, c.Account, c.ID)
		if err != nil {
			return false, err
		}
		if data == nil {
			return false, ErrHistoryUnknown
		}
	}
	return false, storage.ErrConflict
}
