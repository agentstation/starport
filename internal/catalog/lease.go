package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/agentstation/starmap"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"

	"github.com/agentstation/starport/internal/storage"
)

// leaseKey is the shared-storage key of the catalog runtime lease. Every
// instance of one deployment reads and writes the same key, so the lease is
// the fence that keeps two instances from committing the same head.
const leaseKey = "catalog:runtime:lease"

// LeaseStore is the catalog runtime lease over Starport's shared storage. It
// gives this deployment the epoch that fences an accepted head: an acceptance
// that started under an older epoch belongs to an instance that has since lost
// the lease, and shared storage must refuse it.
type LeaseStore struct {
	store storage.KVStore
	now   func() time.Time
}

// leaseRecord is the durable form of one lease.
type leaseRecord struct {
	Holder    string    `json:"holder"`
	Epoch     uint64    `json:"epoch"`
	ExpiresAt time.Time `json:"expires_at"`
}

// NewLeaseStore returns the catalog runtime lease over shared storage.
func NewLeaseStore(store storage.KVStore) (*LeaseStore, error) {
	if store == nil {
		return nil, errors.New("catalog lease storage is required")
	}
	return &LeaseStore{store: store, now: time.Now}, nil
}

// withClock replaces the lease clock. Tests drive expiry without a real wait.
func (l *LeaseStore) withClock(now func() time.Time) *LeaseStore {
	if now != nil {
		l.now = now
	}
	return l
}

// AcquireLease takes the lease for one holder. A live lease another holder
// owns returns a conflict, which names a non-owner state and never a failure.
// Every fresh acquisition raises the epoch, so an older epoch is stale.
func (l *LeaseStore) AcquireLease(
	ctx context.Context,
	holder string,
	ttl time.Duration,
) (starmap.Lease, error) {
	if l == nil || l.store == nil {
		return starmap.Lease{}, errors.New("catalog lease storage is required")
	}
	if strings.TrimSpace(holder) == "" {
		return starmap.Lease{}, &starmaperrors.ValidationError{
			Field: "lease.holder", Message: "is required",
		}
	}
	if ttl <= 0 {
		return starmap.Lease{}, &starmaperrors.ValidationError{
			Field: "lease.ttl", Value: ttl, Message: "must be positive",
		}
	}
	current, raw, err := l.read(ctx)
	if err != nil {
		return starmap.Lease{}, err
	}
	now := l.now().UTC()
	if raw != nil && current.Holder != holder && current.ExpiresAt.After(now) {
		return starmap.Lease{}, &starmaperrors.ConflictError{
			Resource: "catalog runtime lease",
			Expected: holder,
			Actual:   current.Holder,
			Message:  "another instance holds the catalog runtime lease",
		}
	}
	next := leaseRecord{
		Holder:    holder,
		Epoch:     current.Epoch + 1,
		ExpiresAt: now.Add(ttl),
	}
	if err := l.swap(ctx, raw, next); err != nil {
		return starmap.Lease{}, err
	}
	return next.lease(), nil
}

// Renew extends a held lease and keeps its epoch. A lease another holder took
// returns a conflict.
func (l *LeaseStore) Renew(
	ctx context.Context,
	lease starmap.Lease,
	ttl time.Duration,
) (starmap.Lease, error) {
	if l == nil || l.store == nil {
		return starmap.Lease{}, errors.New("catalog lease storage is required")
	}
	if ttl <= 0 {
		return starmap.Lease{}, &starmaperrors.ValidationError{
			Field: "lease.ttl", Value: ttl, Message: "must be positive",
		}
	}
	current, raw, err := l.read(ctx)
	if err != nil {
		return starmap.Lease{}, err
	}
	if raw == nil || current.Holder != lease.Holder || current.Epoch != lease.Epoch {
		return starmap.Lease{}, &starmaperrors.ConflictError{
			Resource: "catalog runtime lease",
			Expected: leaseIdentity(lease.Holder, lease.Epoch),
			Actual:   leaseIdentity(current.Holder, current.Epoch),
			Message:  "the catalog runtime lease moved to another holder",
		}
	}
	next := leaseRecord{
		Holder:    current.Holder,
		Epoch:     current.Epoch,
		ExpiresAt: l.now().UTC().Add(ttl),
	}
	if err := l.swap(ctx, raw, next); err != nil {
		return starmap.Lease{}, err
	}
	return next.lease(), nil
}

// Release returns the lease early. A lease another holder took stays as it is.
func (l *LeaseStore) Release(ctx context.Context, lease starmap.Lease) error {
	if l == nil || l.store == nil {
		return errors.New("catalog lease storage is required")
	}
	current, raw, err := l.read(ctx)
	if err != nil {
		return err
	}
	if raw == nil || current.Holder != lease.Holder || current.Epoch != lease.Epoch {
		return nil
	}
	released := leaseRecord{Holder: "", Epoch: current.Epoch, ExpiresAt: time.Time{}}
	return l.swap(ctx, raw, released)
}

// CurrentEpoch returns the epoch of the lease shared storage holds. A store
// that holds no lease reports epoch zero, and every candidate then passes the
// fence.
func (l *LeaseStore) CurrentEpoch(ctx context.Context) (uint64, error) {
	if l == nil || l.store == nil {
		return 0, errors.New("catalog lease storage is required")
	}
	current, _, err := l.read(ctx)
	if err != nil {
		return 0, err
	}
	return current.Epoch, nil
}

func (l *LeaseStore) read(ctx context.Context) (leaseRecord, []byte, error) {
	raw, err := l.store.Get(ctx, leaseKey)
	switch {
	case err == nil:
	case errors.Is(err, storage.ErrNotFound):
		return leaseRecord{}, nil, nil
	default:
		return leaseRecord{}, nil, fmt.Errorf("read catalog runtime lease: %w", err)
	}
	var record leaseRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return leaseRecord{}, nil, fmt.Errorf("decode catalog runtime lease: %w", err)
	}
	return record, raw, nil
}

func (l *LeaseStore) swap(ctx context.Context, previous []byte, next leaseRecord) error {
	encoded, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("encode catalog runtime lease: %w", err)
	}
	if err := l.store.CompareAndSwap(ctx, leaseKey, previous, encoded); err != nil {
		return &starmaperrors.ConflictError{
			Resource: "catalog runtime lease",
			Expected: leaseIdentity(next.Holder, next.Epoch),
			Message:  "another instance changed the catalog runtime lease",
		}
	}
	return nil
}

func (r leaseRecord) lease() starmap.Lease {
	return starmap.Lease{Holder: r.Holder, Epoch: r.Epoch, ExpiresAt: r.ExpiresAt}
}

func leaseIdentity(holder string, epoch uint64) string {
	return holder + "@" + strconv.FormatUint(epoch, 10)
}

var _ starmap.LeaseStore = (*LeaseStore)(nil)
