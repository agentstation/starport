package catalog

import (
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/storage"
)

const (
	fleetRetentionLockLifetime = 30 * time.Second
	fleetRetentionAttempts     = 64
	fleetRetentionMaxBytes     = 2 << 30
	fleetRetentionMaxEntries   = 96
	fleetRetentionMaxReaders   = 256
	fleetRetentionRecordBytes  = 16 << 20
)

type fleetBlob struct {
	ID              string            `json:"id"`
	Head            runtime.FleetHead `json:"head"`
	Record          generationRecord  `json:"record"`
	GenerationBytes int64             `json:"generation_bytes"`
	RecoveryBytes   int64             `json:"recovery_bytes"`
}

type fleetInventory struct {
	Version int               `json:"version"`
	Entries []fleetBlob       `json:"entries"`
	Pending *fleetBlob        `json:"pending,omitempty"`
	Readers map[string]string `json:"readers"`
}

// fleetMaintenance serializes payload operations. Every write checks its native lease.
// The durable pending entry names all chunks before any chunk write or deletion.
type fleetMaintenance struct {
	owner     *FleetStore
	token     []byte
	inventory fleetInventory
	encoded   []byte
}

func (s *FleetStore) maintain(ctx context.Context) (*fleetMaintenance, error) {
	return s.beginMaintenance(ctx, true)
}

func (s *FleetStore) beginMaintenance(ctx context.Context, recoverPending bool) (*fleetMaintenance, error) {
	if err := s.checkApproval(ctx); err != nil {
		return nil, err
	}
	token := []byte(rand.Text())
	for range fleetRetentionAttempts {
		err := s.store.CompareAndSwap(ctx, []storage.CompareAndSwapMutation{{Key: s.prefix + "maintenance", NewValue: token, TTL: fleetRetentionLockLifetime}})
		if err == nil {
			m := &fleetMaintenance{owner: s, token: token}
			if err := m.load(ctx); err != nil {
				return nil, errors.Join(err, m.close())
			}
			if recoverPending {
				if err := m.recover(ctx); err != nil {
					return nil, errors.Join(err, m.close())
				}
			}
			return m, nil
		}
		if !errors.Is(err, storage.ErrConflict) {
			return nil, err
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, fleetStoreConflict("catalog maintenance is busy; retry the operation")
}

func (m *fleetMaintenance) mutate(ctx context.Context, mutations []storage.CompareAndSwapMutation, live ...string) error {
	key := m.owner.prefix + "maintenance"
	mutations = append(mutations, storage.CompareAndSwapMutation{Key: key, ExpectedValue: m.token, NewValue: m.token, TTL: fleetRetentionLockLifetime})
	return m.owner.store.CompareAndSwap(ctx, mutations, append(live, key)...)
}

// finish releases the maintenance lease after an operation has its durable result.
// A lost release response cannot undo that result. Native expiry bounds the next wait.
func (m *fleetMaintenance) finish() {
	_ = m.close()
}

func (m *fleetMaintenance) close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := m.owner.store.CompareAndSwap(ctx, []storage.CompareAndSwapMutation{{Key: m.owner.prefix + "maintenance", ExpectedValue: m.token}})
	if errors.Is(err, storage.ErrConflict) {
		return nil
	}
	return err
}

func (m *fleetMaintenance) load(ctx context.Context) error {
	encoded, _, err := m.owner.store.ReadWithLifetime(ctx, m.owner.prefix+"inventory", fleetRetentionRecordBytes)
	if errors.Is(err, storage.ErrNotFound) {
		_, _, guardErr := m.owner.store.ReadWithLifetime(ctx, m.owner.prefix+"inventory-initialized", 64)
		if !errors.Is(guardErr, storage.ErrNotFound) {
			return errors.New("initialized fleet storage lost its inventory or its state is uncertain")
		}
		_, _, headErr := m.owner.store.ReadWithLifetime(ctx, m.owner.prefix+"head", 4096)
		if !errors.Is(headErr, storage.ErrNotFound) {
			return errors.New("fleet inventory is missing from populated or uncertain storage")
		}
		m.inventory = fleetInventory{Version: 1, Readers: map[string]string{}}
		return nil
	}
	if err != nil {
		return err
	}
	if err = json.Unmarshal(encoded, &m.inventory); err != nil {
		return err
	}
	if err := m.inventory.validate(m.owner.identity); err != nil {
		return err
	}
	m.encoded = encoded
	return nil
}

func (m *fleetMaintenance) save(ctx context.Context, extra ...storage.CompareAndSwapMutation) error {
	if err := m.inventory.validate(m.owner.identity); err != nil {
		return err
	}
	encoded, err := json.Marshal(m.inventory)
	if err != nil {
		return err
	}
	if len(encoded) > fleetRetentionRecordBytes {
		return errors.New("fleet retention inventory exceeds its byte limit")
	}
	mutations := append(extra, storage.CompareAndSwapMutation{Key: m.owner.prefix + "inventory", ExpectedValue: m.encoded, NewValue: encoded})
	if m.encoded == nil {
		mutations = append(mutations, storage.CompareAndSwapMutation{Key: m.owner.prefix + "inventory-initialized", NewValue: []byte("fleet-retention/1")})
	}
	if err = m.mutate(ctx, mutations); err != nil {
		return err
	}
	m.encoded = encoded
	return nil
}

func (m *fleetMaintenance) chunkKey(blob fleetBlob, digest string) string {
	return m.owner.prefix + "blob:" + blob.ID + ":" + digest
}

// recover finishes an interrupted upload or deletion before another operation writes chunks.
func (m *fleetMaintenance) recover(ctx context.Context) error {
	if m.inventory.Pending == nil {
		return nil
	}
	blob := *m.inventory.Pending
	head, _, err := m.readHead(ctx)
	if err != nil {
		return err
	}
	accepted, _, err := m.owner.readAcceptance(ctx)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	if blob.Head == head || blob.Head == accepted.Head {
		return errors.New("pending deletion selects a protected fleet head")
	}
	for _, entry := range accepted.History {
		if entry.GenerationID == blob.Head.GenerationID {
			if _, retained := m.generation(entry.GenerationID); !retained {
				return errors.New("pending deletion selects protected rollback content")
			}
		}
	}
	receiptKey := m.owner.publicationKey(blob.Head)
	receipt, _, err := m.owner.store.ReadWithLifetime(ctx, receiptKey, fleetDescriptorMaxBytes)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	if err == nil {
		var retained fleetBlob
		if err := json.Unmarshal(receipt, &retained); err != nil {
			return err
		}
		if !reflect.DeepEqual(retained, blob) {
			return errors.New("pending fleet receipt differs from its ownership record")
		}
		if err = m.mutate(ctx, []storage.CompareAndSwapMutation{{Key: receiptKey, ExpectedValue: receipt}}); err != nil {
			return err
		}
	}
	for _, digest := range blob.Record.Chunks {
		key := m.chunkKey(blob, digest)
		value, _, err := m.owner.store.ReadWithLifetime(ctx, key, generationChunkSize)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if err = m.mutate(ctx, []storage.CompareAndSwapMutation{{Key: key, ExpectedValue: value}}); err != nil {
			return err
		}
	}
	m.inventory.Pending = nil
	return m.save(ctx)
}

func (m *fleetMaintenance) publication(head runtime.FleetHead) (fleetBlob, bool) {
	i := slices.IndexFunc(m.inventory.Entries, func(b fleetBlob) bool { return b.Head == head })
	if i < 0 {
		return fleetBlob{}, false
	}
	return m.inventory.Entries[i], true
}

func (m *fleetMaintenance) generation(id string) (fleetBlob, bool) {
	for _, entry := range slices.Backward(m.inventory.Entries) {
		if entry.Head.GenerationID == id {
			return entry, true
		}
	}
	return fleetBlob{}, false
}

func (m *fleetMaintenance) read(ctx context.Context, blob fleetBlob) (runtime.FleetSnapshot, error) {
	data, err := m.owner.readBlob(ctx, blob)
	if err != nil {
		return runtime.FleetSnapshot{}, err
	}
	var snapshot runtime.FleetSnapshot
	if err = json.Unmarshal(data, &snapshot); err != nil {
		return snapshot, err
	}
	if err = snapshot.Validate(); err != nil {
		return snapshot, err
	}
	if snapshot.Head != blob.Head {
		return snapshot, fleetStoreConflict("the stored snapshot differs from the selected head")
	}
	size, err := fleetGenerationBytes(snapshot.Publication.Generation)
	if err != nil {
		return snapshot, err
	}
	if blob.GenerationBytes != size || blob.RecoveryBytes != int64(len(snapshot.Publication.Recovery.Data)) {
		return snapshot, errors.New("fleet retained byte counts differ from the stored snapshot")
	}
	return snapshot, nil
}

func (m *fleetMaintenance) readHead(ctx context.Context) (runtime.FleetHead, []byte, error) {
	value, _, err := m.owner.store.ReadWithLifetime(ctx, m.owner.prefix+"head", 4096)
	if errors.Is(err, storage.ErrNotFound) {
		return runtime.FleetHead{}, nil, nil
	}
	if err != nil {
		return runtime.FleetHead{}, nil, err
	}
	var h runtime.FleetHead
	if err = json.Unmarshal(value, &h); err != nil {
		return h, nil, err
	}
	if err = h.Validate(); err != nil {
		return h, nil, err
	}
	if h.Identity != m.owner.identity {
		return h, nil, fleetStoreConflict("the catalog head belongs to another recovery identity")
	}
	return h, value, nil
}

// AcquireGeneration keeps a retained generation until an explicit successful release.
// Reader claims have no time-based expiry. Recovery must resolve abandoned claims explicitly.
func (s *FleetStore) AcquireGeneration(ctx context.Context, id string) (catalogs.Generation, func() error, error) {
	m, err := s.maintain(ctx)
	if err != nil {
		return catalogs.Generation{}, nil, err
	}
	defer m.finish()
	blob, ok := m.generation(id)
	if !ok {
		return catalogs.Generation{}, nil, &starmaperrors.NotFoundError{Resource: "fleet generation", ID: id}
	}
	if len(m.inventory.Readers) >= fleetRetentionMaxReaders {
		return catalogs.Generation{}, nil, errors.New("fleet reader retention is full; release or recover reader claims")
	}
	token := rand.Text()
	m.inventory.Readers[token] = blob.ID
	if err = m.save(ctx); err != nil {
		return catalogs.Generation{}, nil, err
	}
	snapshot, err := m.read(ctx, blob)
	if err != nil {
		delete(m.inventory.Readers, token)
		return catalogs.Generation{}, nil, errors.Join(err, m.save(ctx))
	}
	var mu sync.Mutex
	released := false
	release := func() error {
		mu.Lock()
		defer mu.Unlock()
		if released {
			return nil
		}
		releaseCtx, cancel := context.WithTimeout(context.Background(), fleetRetentionLockLifetime)
		defer cancel()
		held, err := s.maintain(releaseCtx)
		if err != nil {
			return err
		}
		defer held.finish()
		delete(held.inventory.Readers, token)
		err = held.save(releaseCtx)
		if err == nil {
			released = true
		}
		return err
	}
	return snapshot.Publication.Generation, release, nil
}

func (m *fleetMaintenance) capacity(encoded int) error {
	total := int64(encoded)
	for _, b := range m.inventory.Entries {
		total += int64(b.Record.Size)
	}
	if len(m.inventory.Entries) >= fleetRetentionMaxEntries || total > fleetRetentionMaxBytes {
		return fmt.Errorf("fleet retention capacity reached: %d publications, %d bytes; run collection or release protected generations", len(m.inventory.Entries), total)
	}
	return nil
}
