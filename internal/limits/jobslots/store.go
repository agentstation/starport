// Package jobslots owns durable outstanding-work claims for videos and batches.
package jobslots

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/storage"
)

var (
	// ErrInvalid reports an incomplete claim identity or invalid stored record.
	ErrInvalid = errors.New("job slots: invalid claim")
	// ErrHistoryUnknown requires recovery of missing or anonymous ownership.
	ErrHistoryUnknown = limits.ErrOutstandingJobsRecoveryRequired
	// ErrClaimConflict refuses another operation under an existing claim ID.
	ErrClaimConflict = errors.New("job slots: claim identity conflict")
	// ErrClaimReleased refuses reuse of a completed claim.
	ErrClaimReleased = errors.New("job slots: claim already released")
	// ErrClaimNotFound refuses release without a durable claim.
	ErrClaimNotFound = errors.New("job slots: claim not found")
)

const (
	recordVersion  = 3
	maxRecordBytes = 8192
	maxAttempts    = 64
	claimPrefix    = "limits:v2:job_claims:"
)

// Store changes each claim and its account count in one native transaction.
// Claims have no automatic expiry. Recovery owns their eventual collection.
type Store struct {
	store           storage.KVStore
	now             func() time.Time
	recoveryMu      sync.Mutex
	recoveryCursor  string
	recoveryNext    string
	recoveryPending []string
	recoveryLoaded  bool
}

// Claim binds one outstanding slot to a gateway job.
type Claim struct {
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	Attached  bool      `json:"attached,omitzero"`
	Account   string    `json:"account"`
	ID        string    `json:"id"`
	JobID     string    `json:"job_id"`
	Kind      string    `json:"kind"`
	Bound     int64     `json:"bound"`
	Released  bool      `json:"released,omitzero"`
}

type counter struct {
	Version int   `json:"version"`
	Total   int64 `json:"total"`
}

// Open requires native conditional batches and starts no background work.
func Open(store storage.KVStore) (*Store, error) {
	if store == nil {
		return nil, limits.ErrCounterRequired
	}
	return &Store{store: store, now: time.Now}, nil
}

// Reserve claims one slot. Exact retries cannot increase its account count.
// A nonpositive bound permits unbounded work while preserving its ownership.
func (s *Store) Reserve(ctx context.Context, account, id, jobID, kind string, bound int64) error {
	candidate := Claim{Version: recordVersion, CreatedAt: s.now().UTC(), Account: account, ID: id, JobID: jobID, Kind: kind, Bound: max(bound, 0)}
	if !candidate.valid() {
		return ErrInvalid
	}
	for range maxAttempts {
		held, oldClaim, err := s.readClaim(ctx, account, id)
		if err != nil {
			return err
		}
		count, oldCount, err := s.readCounter(ctx, account)
		if err != nil {
			return err
		}
		if oldClaim != nil {
			if oldCount == nil {
				return ErrHistoryUnknown
			}
			released := held.Released
			held.Released = false
			held.Attached = false
			candidate.CreatedAt = held.CreatedAt
			if held != candidate {
				return ErrClaimConflict
			}
			if released {
				return ErrClaimReleased
			}
			if count.Total == 0 {
				return ErrHistoryUnknown
			}
			return nil
		}
		if candidate.Bound > 0 && count.Total >= candidate.Bound {
			return fmt.Errorf("%w: %d outstanding jobs reach the %d outstanding job bound", limits.ErrTooManyOutstandingJobs, count.Total, candidate.Bound)
		}
		if count.Total == math.MaxInt64 {
			return ErrInvalid
		}
		count.Total++
		err = s.write(ctx, candidate, oldClaim, count, oldCount)
		if !errors.Is(err, storage.ErrConflict) {
			return err
		}
	}
	return storage.ErrConflict
}

// Release closes one claim and decrements its count at most once.
// A lost acknowledgement can retry the same claim without releasing other work.
func (s *Store) Release(ctx context.Context, account, id string) error {
	if !validID(account) || !validID(id) {
		return ErrInvalid
	}
	for range maxAttempts {
		held, oldClaim, err := s.readClaim(ctx, account, id)
		if err != nil {
			return err
		}
		if oldClaim == nil {
			return ErrClaimNotFound
		}
		count, oldCount, err := s.readCounter(ctx, account)
		if err != nil {
			return err
		}
		if oldCount == nil {
			return ErrHistoryUnknown
		}
		if held.Released {
			return nil
		}
		if count.Total <= 0 {
			_, current, err := s.readClaim(ctx, account, id)
			if err != nil {
				return err
			}
			if !bytes.Equal(current, oldClaim) {
				continue
			}
			return ErrHistoryUnknown
		}
		held.Released = true
		count.Total--
		err = s.write(ctx, held, oldClaim, count, oldCount)
		if !errors.Is(err, storage.ErrConflict) {
			return err
		}
	}
	return storage.ErrConflict
}

// Get reads a claim for reconciliation without granting permission to dispatch.
func (s *Store) Get(ctx context.Context, account, id string) (Claim, error) {
	if !validID(account) || !validID(id) {
		return Claim{}, ErrInvalid
	}
	c, data, err := s.readClaim(ctx, account, id)
	if err != nil {
		return Claim{}, err
	}
	if data == nil {
		return Claim{}, ErrClaimNotFound
	}
	return c, nil
}

// Total reads the durable count without changing it.
func (s *Store) Total(ctx context.Context, account string) (int64, error) {
	if !validID(account) {
		return 0, ErrInvalid
	}
	count, _, err := s.readCounter(ctx, account)
	return count.Total, err
}

func (s *Store) write(ctx context.Context, c Claim, oldClaim []byte, count counter, oldCount []byte) error {
	record, err := json.Marshal(c)
	if err != nil {
		return err
	}
	total, err := json.Marshal(count)
	if err != nil {
		return err
	}
	return s.store.CompareAndSwapBatch(ctx, []storage.CompareAndSwapMutation{
		{Key: countKey(c.Account), ExpectedValue: oldCount, NewValue: total},
		{Key: claimKey(c.Account, c.ID), ExpectedValue: oldClaim, NewValue: record},
		{Key: historyKey(c.Account), ExpectedValue: historyExpected(oldCount), NewValue: []byte("3")},
	})
}

func (s *Store) readCounter(ctx context.Context, account string) (counter, []byte, error) {
	data, err := s.store.GetBounded(ctx, countKey(account), maxRecordBytes)
	missing := errors.Is(err, storage.ErrNotFound)
	if err != nil && !missing {
		return counter{}, nil, err
	}
	history, historyErr := s.store.GetBounded(ctx, historyKey(account), 16)
	if missing {
		if errors.Is(historyErr, storage.ErrNotFound) {
			keys, scanErr := s.store.ScanWithPrefix(ctx, accountClaimPrefix(account), 1)
			if scanErr != nil {
				return counter{}, nil, scanErr
			}
			if len(keys) == 0 {
				return counter{Version: recordVersion}, nil, nil
			}
			history, historyErr = s.store.GetBounded(ctx, historyKey(account), 16)
		}
		if historyErr != nil {
			return counter{}, nil, errors.Join(ErrHistoryUnknown, historyErr)
		}
		// Another claimant may initialize both keys between these reads.
		data, err = s.store.GetBounded(ctx, countKey(account), maxRecordBytes)
		if err != nil {
			return counter{}, nil, errors.Join(ErrHistoryUnknown, err)
		}
	}
	if historyErr != nil {
		return counter{}, nil, errors.Join(ErrHistoryUnknown, historyErr)
	}
	if !bytes.Equal(history, []byte("3")) {
		return counter{}, nil, ErrHistoryUnknown
	}
	var count counter
	if err := json.Unmarshal(data, &count); err != nil || count.Version != recordVersion || count.Total < 0 {
		return counter{}, nil, ErrHistoryUnknown
	}
	return count, data, nil
}

func (s *Store) readClaim(ctx context.Context, account, id string) (Claim, []byte, error) {
	data, err := s.store.GetBounded(ctx, claimKey(account, id), maxRecordBytes)
	if errors.Is(err, storage.ErrNotFound) {
		return Claim{}, nil, nil
	}
	if err != nil {
		return Claim{}, nil, err
	}
	var c Claim
	if err := json.Unmarshal(data, &c); err != nil || !c.valid() || c.Account != account || c.ID != id {
		return Claim{}, nil, ErrInvalid
	}
	return c, data, nil
}

func (c Claim) valid() bool {
	return c.Version == recordVersion && !c.CreatedAt.IsZero() && validID(c.Account) && validID(c.ID) && validID(c.JobID) && (c.Kind == "video" || c.Kind == "batch") && c.Bound >= 0
}
func validID(s string) bool { return strings.TrimSpace(s) != "" && len(s) <= 512 }

// Reuse the scalar key so an older writer cannot mutate the new counter format.
// Existing scalar values require migration instead of an inferred zero count.
func countKey(account string) string { return limits.OutstandingJobsPrefix + account }
func claimKey(account, id string) string {
	return accountClaimPrefix(account) + base64.RawURLEncoding.EncodeToString([]byte(id))
}

func historyKey(account string) string {
	return accountClaimPrefix(account) + "history"
}
func historyExpected(oldCount []byte) []byte {
	if oldCount == nil {
		return nil
	}
	return []byte("3")
}

func accountClaimPrefix(account string) string {
	return claimPrefix + base64.RawURLEncoding.EncodeToString([]byte(account)) + ":"
}
