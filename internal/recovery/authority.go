package recovery

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"time"

	"github.com/agentstation/starport/internal/storage"
)

const authorityKey = "recovery:authority:v1"

type authorityRecord struct {
	Version     int    `json:"version"`
	Approval    Record `json:"approval"`
	OperationID string `json:"operation_id"`
}

func authorityBytes(approval Record, operation string) ([]byte, error) {
	return json.Marshal(authorityRecord{Version: 1, Approval: approval, OperationID: operation})
}

// Authority binds budget writes to independent approval and its native epoch.
// Reads alone grant no admission. Every mutation checks SQL before and after
// the native write, which also checks the epoch and backend incarnation.
type Authority struct {
	store    storage.TimeBoundStore
	witness  *Witness
	approval Record
	guard    []byte
}

// OpenAuthority reads existing approval. It never creates or repairs a gate.
// A missing native record requires explicit recovery, including on an unchanged
// Valkey process. Observing a process identity cannot replace approval.
func (w *Witness) OpenAuthority(ctx context.Context, backend storage.IncarnationProvider, deployment string) (*Authority, error) {
	if backend == nil {
		return nil, ErrClosed
	}
	approved, err := w.Approved(ctx, deployment)
	if err != nil {
		return nil, err
	}
	bound, err := backend.BindIncarnation(ctx, approved.BackendID)
	if err != nil {
		return nil, err
	}
	store, ok := bound.(storage.TimeBoundStore)
	if !ok {
		return nil, ErrClosed
	}
	guard, ttl, err := store.ReadWithLifetime(ctx, authorityKey, 8192)
	if err != nil {
		return nil, errors.Join(ErrClosed, err)
	}
	var record authorityRecord
	if ttl != 0 || json.Unmarshal(guard, &record) != nil || record.Version != 1 || record.Approval != approved || record.OperationID == "" {
		return nil, ErrClosed
	}
	authority := &Authority{store: store, witness: w, approval: approved, guard: guard}
	if err := authority.Check(ctx); err != nil {
		return nil, err
	}
	return authority, nil
}

// Check verifies the original independent approval without adopting a newer epoch.
// Closing recovery still requires external fencing of unreachable gateways.
func (a *Authority) Check(ctx context.Context) error {
	current, err := a.witness.Approved(ctx, a.approval.DeploymentID)
	if err != nil {
		return err
	}
	if current != a.approval {
		return ErrConflict
	}
	return nil
}

// ReadWithLifetime reads incarnation-bound data without authorizing a write.
func (a *Authority) ReadWithLifetime(ctx context.Context, key string, maxBytes int) ([]byte, time.Duration, error) {
	return a.store.ReadWithLifetime(ctx, key, maxBytes)
}

// AuthorityTime reads the approved backend's clock. It grants no dispatch permit.
func (a *Authority) AuthorityTime(ctx context.Context) (time.Time, error) {
	return a.store.AuthorityTime(ctx)
}

// CompareAndSwapInWindow checks the native recovery epoch with all budget writes.
// Lost acknowledgements and changed SQL approval can return an error after commit.
// Callers must retain uncertain capacity and must not dispatch on those errors.
func (a *Authority) CompareAndSwapInWindow(ctx context.Context, mutations []storage.CompareAndSwapMutation, window storage.TimeWindow) error {
	if err := a.Check(ctx); err != nil {
		return err
	}
	guarded := make([]storage.CompareAndSwapMutation, 0, len(mutations)+1)
	for _, mutation := range mutations {
		if mutation.Key == authorityKey {
			return storage.ErrInvalidMutation
		}
	}
	guarded = append(guarded, storage.CompareAndSwapMutation{Key: authorityKey, ExpectedValue: a.guard, NewValue: a.guard})
	guarded = append(guarded, mutations...)
	if err := a.store.CompareAndSwapInWindow(ctx, guarded, window); err != nil {
		return err
	}
	return a.Check(ctx)
}

// ApproveAuthority installs a reconciled native epoch before opening SQL approval.
// The operator must first stop admission, fence old writers, and reconcile lost
// history. This operation does not perform or prove those external steps.
// Partial results stay closed. An exact retry can finish the same operation.
func (w *Witness) ApproveAuthority(ctx context.Context, backend storage.IncarnationProvider, expected Record, identity, evidence, operation string) (Record, error) {
	if backend == nil || expected.Open || expected.Epoch <= 0 || (FreshRequest{OperationID: operation, Evidence: evidence}).Validate() != nil {
		return Record{}, ErrConflict
	}
	next := expected
	next.Open, next.BackendID, next.Evidence = true, identity, evidence
	encoded, err := authorityBytes(next, operation)
	if err != nil {
		return Record{}, err
	}
	current, err := w.Current(ctx, expected.DeploymentID)
	if err != nil {
		return Record{}, err
	}
	if current != expected && current != next {
		return Record{}, ErrConflict
	}
	bound, err := backend.BindIncarnation(ctx, identity)
	if err != nil {
		return Record{}, err
	}
	previous, ttl, err := bound.ReadWithLifetime(ctx, authorityKey, 8192)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return Record{}, err
	}
	if ttl != 0 {
		return Record{}, ErrConflict
	}
	if bytes.Equal(previous, encoded) {
		if current == next {
			return next, nil
		}
	} else {
		if current == next {
			return Record{}, ErrConflict
		}
		if previous != nil {
			var prior authorityRecord
			if json.Unmarshal(previous, &prior) != nil || prior.Version != 1 || prior.Approval.DeploymentID != expected.DeploymentID || prior.Approval.Epoch >= expected.Epoch {
				return Record{}, ErrConflict
			}
		}
		if err := bound.CompareAndSwap(ctx, []storage.CompareAndSwapMutation{{Key: authorityKey, ExpectedValue: previous, NewValue: encoded}}); err != nil {
			return Record{}, err
		}
	}
	return w.Approve(ctx, expected, identity, evidence)
}

var _ storage.TimeBoundStore = (*Authority)(nil)
