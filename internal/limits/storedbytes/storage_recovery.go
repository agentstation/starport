package storedbytes

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/storage"
)

// StoredBytesPendingGrace bounds unattached preparation. Recovery competes with
// attachment on the same claim, so a delayed writer cannot attach a closed claim.
const StoredBytesPendingGrace = 10 * time.Minute

// RecoverPending closes abandoned preparations. Attached files remain under
// the file owner's cleanup protocol. Each pass reads native pages for at most
// thirty seconds. The next sweep resumes at the retained cursor.
func (m *StorageMeter) RecoverPending(ctx context.Context) error {
	if !m.recoveryMu.TryLock() {
		return storage.ErrConflict
	}
	defer m.recoveryMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cutoff := time.Now().UTC().Add(-StoredBytesPendingGrace)
	var failures error
	for {
		if err := ctx.Err(); err != nil {
			return errors.Join(failures, err)
		}
		if !m.recoveryLoaded {
			page, err := m.store.ScanPage(ctx, StoredBytesPrefix, m.recoveryCursor, 256)
			if err != nil {
				return errors.Join(failures, err)
			}
			m.recoveryPending, m.recoveryNext, m.recoveryLoaded = page.Keys, page.Next, true
		}
		for len(m.recoveryPending) > 0 {
			if err := ctx.Err(); err != nil {
				return errors.Join(failures, err)
			}
			key := m.recoveryPending[0]
			if err := m.recoverByteClaim(ctx, key, cutoff); err != nil && failures == nil {
				failures = fmt.Errorf("stored bytes: recovery: %w", err)
			}
			m.recoveryPending[0] = ""
			m.recoveryPending = m.recoveryPending[1:]
		}
		m.recoveryCursor, m.recoveryLoaded, m.recoveryPending = m.recoveryNext, false, nil
		if m.recoveryCursor == "" {
			return failures
		}
	}
}

func (m *StorageMeter) recoverByteClaim(ctx context.Context, key string, cutoff time.Time) error {
	if strings.HasSuffix(key, ":total") {
		return nil
	}
	holder, id, ok := strings.Cut(strings.TrimPrefix(key, StoredBytesPrefix), ":claim:")
	if !ok {
		return ErrStorageHistoryUnknown
	}
	account, err := base64.RawURLEncoding.DecodeString(holder)
	if err != nil {
		return ErrStorageHistoryUnknown
	}
	identity, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		return ErrStorageHistoryUnknown
	}
	claim, _, err := m.readClaim(ctx, string(account), string(identity))
	if err != nil {
		return err
	}
	if claim.Attached || claim.Released || claim.CreatedAt.After(cutoff) {
		return nil
	}
	err = m.Abort(ctx, claim.Holder, claim.ID)
	if errors.Is(err, ErrStorageClaimConflict) {
		current, _, readErr := m.readClaim(ctx, claim.Holder, claim.ID)
		if readErr != nil {
			return readErr
		}
		if current.Attached || current.Released {
			return nil
		}
	}
	return err
}
