package reservation

import (
	"context"
	"errors"
	"strings"

	"github.com/agentstation/starport/internal/storage"
)

const attemptPrefix = "budget:v1:attempt:"

// Scanner enumerates identifiers. It grants no authority to read or change balances.
type Scanner interface {
	ScanPage(context.Context, string, string, int) (storage.KeyPage, error)
}

// Recovery retries durable measured usage through the repository's original
// storage authority. One background worker owns its cursor and calls Pass in
// sequence. Separate replicas can recover the same evidence safely.
type Recovery struct {
	repository *Repository
	scanner    Scanner
	cursor     string
	keys       []string
	end        bool
}

// RecoveryResult counts observations in one bounded pass, not fleet totals.
// Recovered includes exact settlements another worker completed concurrently.
type RecoveryResult struct {
	Scanned   int
	Recovered int
	Held      int
	Failed    int
	Complete  bool
}

// NewRecovery constructs a worker without scanning or starting background work.
// The scanner must enumerate the same logical store as the repository authority.
func NewRecovery(repository *Repository, scanner Scanner) (*Recovery, error) {
	if repository == nil || scanner == nil {
		return nil, ErrInvalid
	}
	return &Recovery{repository: repository, scanner: scanner}, nil
}

// Pass spends at most maxWork scan or record operations. The caller supplies a
// deadline for storage work. Unread keys and the native cursor survive between
// passes, including an oversized native page. Restart safely replays the scan.
// No usage evidence means no refund, even after a deadline or process restart.
func (r *Recovery) Pass(ctx context.Context, maxWork int) (RecoveryResult, error) {
	var result RecoveryResult
	if maxWork < 1 || maxWork > 1000 {
		return result, ErrInvalid
	}
	var firstError error
	for range maxWork {
		if err := ctx.Err(); err != nil {
			return result, errors.Join(firstError, err)
		}
		if len(r.keys) == 0 {
			page, err := r.scanner.ScanPage(ctx, attemptPrefix, r.cursor, min(maxWork, 128))
			if err != nil {
				// Native cursors cannot survive backend replacement. Replaying
				// keys cannot settle twice or change the repository authority.
				r.cursor = ""
				return result, errors.Join(firstError, err)
			}
			r.keys, r.cursor, r.end = page.Keys, page.Next, page.Next == ""
		} else {
			key := r.keys[0]
			recovered, held, err := r.recover(ctx, key)
			if ctx.Err() != nil {
				// Retry this key, including a write with a lost acknowledgement.
				return result, errors.Join(firstError, err, ctx.Err())
			}
			r.keys[0], r.keys = "", r.keys[1:]
			result.Scanned++
			switch {
			case err != nil:
				result.Failed++
				if firstError == nil {
					firstError = err
				}
			case recovered:
				result.Recovered++
			case held:
				result.Held++
			}
		}
		if len(r.keys) == 0 && r.end {
			r.keys, r.cursor, r.end = nil, "", false
			result.Complete = true
			return result, firstError
		}
	}
	return result, firstError
}

func (r *Recovery) recover(ctx context.Context, key string) (recovered, held bool, err error) {
	if !strings.HasPrefix(key, attemptPrefix) {
		return false, false, ErrUnavailable
	}
	record, _, err := r.repository.readRecordKey(ctx, key)
	if err != nil {
		return false, false, err
	}
	if record.Pending != nil {
		err = r.repository.ReconcileRetained(ctx, record.Attempt.ID)
		return err == nil, false, err
	}
	return false, record.State != Settled && record.State != Canceled, nil
}
