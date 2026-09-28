package usage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/agentstation/starport/internal/storage"
)

var (
	// ErrRecordConflict refuses different contents for an existing usage identity.
	ErrRecordConflict = errors.New("usage identity has different contents")
	// ErrRecordExpired refuses replay after the record's retention deadline.
	ErrRecordExpired = errors.New("usage record retention deadline passed")
)

const maxCommitConflicts = 64

type counterChange struct {
	counter string
	key     string
	delta   int64
	expires time.Time
}

// commitRecord uses one native conditional batch for the record and all totals.
// The record itself proves an exact retry. Existing totals keep their expiration.
// Optional usage cannot grant budget capacity or settle a required reservation.
func (r *repository) commitRecord(ctx context.Context, record Record, data []byte) error {
	changes := r.counterChanges(record)
	key := recordKey(record.KeyID, record.Timestamp, record.RequestID)
	keys := make([]string, 1, len(changes)+1)
	keys[0] = key
	for _, change := range changes {
		if change.delta < 0 {
			return ErrInvalidRecord
		}
		keys = append(keys, change.key)
	}
	// Whole-second boundaries match the least precise supported store expiration.
	expires := record.Timestamp.Add(r.retention).Truncate(time.Second)
	for range maxCommitConflicts {
		if err := ctx.Err(); err != nil {
			return err
		}
		values, err := r.store.BatchGet(ctx, keys)
		if err != nil {
			return fmt.Errorf("read usage transaction: %w", err)
		}
		if previous, exists := values[key]; exists {
			if bytes.Equal(previous, data) {
				return nil
			}
			return ErrRecordConflict
		}
		now := time.Now()
		if !now.Before(expires) {
			return ErrRecordExpired
		}
		writes := make([]storage.CompareAndSwapMutation, 1, len(changes)+1)
		// Round up for adapters that truncate TTLs to milliseconds. The receipt
		// must survive until the deadline after which an absent replay refuses.
		ttl := expires.Sub(now)
		if remainder := ttl % time.Millisecond; remainder != 0 {
			ttl += time.Millisecond - remainder
		}
		writes[0] = storage.CompareAndSwapMutation{Key: key, NewValue: data, TTL: ttl}
		for _, change := range changes {
			previous, exists := values[change.key]
			total := int64(0)
			if exists {
				total, err = strconv.ParseInt(string(previous), 10, 64)
				if err != nil || total < 0 {
					return ErrCorruptRecord
				}
			}
			if change.delta > math.MaxInt64-total {
				return fmt.Errorf("%w: usage counter overflow", ErrInvalidRecord)
			}
			mutation := storage.CompareAndSwapMutation{Key: change.key, ExpectedValue: previous, NewValue: []byte(strconv.FormatInt(total+change.delta, 10))}
			if !exists {
				mutation.TTL = change.expires.Sub(now)
			}
			writes = append(writes, mutation)
		}
		err = r.store.CompareAndSwapBatch(ctx, writes)
		if errors.Is(err, storage.ErrConflict) {
			continue
		}
		if err != nil {
			return fmt.Errorf("commit usage transaction: %w", err)
		}
		return nil
	}
	return fmt.Errorf("commit usage transaction: %w", storage.ErrConflict)
}

func (r *repository) counterChanges(record Record) []counterChange {
	counters := []struct {
		name  string
		delta int64
	}{
		{counterRequests, 1}, {counterTokens, record.Tokens.Total}, {counterSpend, record.knownSpendNanoUSD()},
	}
	scopes := []Scope{KeyScope(record.KeyID), GatewayScope()}
	if record.AccountID != "" {
		scopes = append(scopes, AccountScope(record.AccountID))
	}
	if record.TeamID != "" {
		scopes = append(scopes, TeamScope(record.TeamID))
	}
	changes := make([]counterChange, 0, len(scopes)*9)
	for _, scope := range scopes {
		for _, interval := range []string{IntervalDay, IntervalWeek, IntervalMonth} {
			start, end := window(interval, record.Timestamp)
			for _, counter := range counters {
				if counter.delta == 0 && counter.name != counterRequests {
					continue
				}
				changes = append(changes, counterChange{counter: counter.name, key: aggregateKey(scope, interval, start, counter.name), delta: counter.delta, expires: end.Add(r.retention)})
			}
		}
	}
	return changes
}
