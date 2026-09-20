// Package revision owns durable authorization change markers.
package revision

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"errors"
	"math"

	"github.com/agentstation/starport/internal/storage"
)

// StorageKey identifies the durable KV authorization marker.
const StorageKey = "authorization:v1:revision"

// ErrCorrupt reports invalid durable revision evidence.
var ErrCorrupt = errors.New("authorization revision is invalid")

// Stamp identifies one durable authority epoch and its latest mutation.
type Stamp struct {
	Epoch    string `json:"epoch"`
	Sequence uint64 `json:"sequence"`
}

// KV commits an authorization marker with the owning repository's mutations.
// A local fence callback runs before the transaction and ends after its result.
type KV struct {
	store storage.KVStore
	begin func() func()
}

// NewKV binds a store without reading or creating state.
func NewKV(store storage.KVStore, begin func() func()) *KV { return &KV{store: store, begin: begin} }

// Read returns durable evidence. Missing state remains a storage error.
func (k *KV) Read(ctx context.Context) (Stamp, error) {
	stamp, _, err := k.read(ctx)
	return stamp, err
}

func (k *KV) read(ctx context.Context) (Stamp, []byte, error) {
	if err := ctx.Err(); err != nil {
		return Stamp{}, nil, err
	}
	data, err := k.store.GetBounded(ctx, StorageKey, 1024)
	if errors.Is(err, storage.ErrValueTooLarge) {
		return Stamp{}, nil, ErrCorrupt
	}
	if err != nil {
		return Stamp{}, nil, err
	}
	if len(data) > 1024 {
		return Stamp{}, nil, ErrCorrupt
	}
	var stamp Stamp
	if err := json.Unmarshal(data, &stamp); err != nil || stamp.Epoch == "" || len(stamp.Epoch) > 256 || stamp.Sequence == 0 {
		return Stamp{}, nil, ErrCorrupt
	}
	return stamp, data, nil
}

// Initialize creates revision evidence before a deployment enables cached admission.
// Existing deployments must also require every writer to update this marker.
func (k *KV) Initialize(ctx context.Context) (Stamp, error) {
	stamp, _, err := k.read(ctx)
	if !errors.Is(err, storage.ErrNotFound) {
		return stamp, err
	}
	stamp = Stamp{Epoch: rand.Text(), Sequence: 1}
	data, err := json.Marshal(stamp)
	if err != nil {
		return Stamp{}, err
	}
	if err := k.store.CompareAndSwap(ctx, StorageKey, nil, data); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			return k.Read(ctx)
		}
		return Stamp{}, err
	}
	return stamp, nil
}

// Apply atomically publishes the mutation and its revision marker.
// A conflict leaves both unchanged. Marker contention retries within a fixed bound.
func (k *KV) Apply(ctx context.Context, mutations []storage.CompareAndSwapMutation) error {
	if len(mutations) == 0 {
		return storage.ErrInvalidKey
	}
	for _, mutation := range mutations {
		if mutation.Key == StorageKey {
			return storage.ErrInvalidKey
		}
	}
	if k.begin != nil {
		finish := k.begin()
		defer finish()
	}
	for range 32 {
		stamp, previous, err := k.read(ctx)
		if errors.Is(err, storage.ErrNotFound) {
			stamp = Stamp{Epoch: rand.Text()}
		} else if err != nil {
			return err
		}
		if stamp.Sequence == math.MaxUint64 {
			return ErrCorrupt
		}
		stamp.Sequence++
		next, err := json.Marshal(stamp)
		if err != nil {
			return err
		}
		batch := make([]storage.CompareAndSwapMutation, 0, len(mutations)+1)
		batch = append(batch, mutations...)
		batch = append(batch, storage.CompareAndSwapMutation{Key: StorageKey, ExpectedValue: previous, NewValue: next})
		err = k.store.CompareAndSwapBatch(ctx, batch)
		if !errors.Is(err, storage.ErrConflict) {
			return err
		}
		_, latest, readErr := k.read(ctx)
		if errors.Is(readErr, storage.ErrNotFound) {
			return storage.ErrConflict
		}
		if readErr != nil {
			return readErr
		}
		if bytes.Equal(previous, latest) {
			return storage.ErrConflict
		}
	}
	return storage.ErrConflict
}
