package catalog

import (
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

// lostAdoptionReply loses only the response to a real atomic selection commit.
type lostAdoptionReply struct {
	storage.IncarnationProvider
	mu       sync.Mutex
	injected bool
	marker   string
}

type lostAdoptionStore struct {
	storage.IncarnationStore
	owner *lostAdoptionReply
}

func (p *lostAdoptionReply) BindIncarnation(ctx context.Context, identity string) (storage.IncarnationStore, error) {
	bound, err := p.IncarnationProvider.BindIncarnation(ctx, identity)
	if err != nil {
		return nil, err
	}
	return &lostAdoptionStore{IncarnationStore: bound, owner: p}, nil
}

func (s *lostAdoptionStore) CompareAndSwap(ctx context.Context, mutations []storage.CompareAndSwapMutation, live ...string) error {
	if err := s.IncarnationStore.CompareAndSwap(ctx, mutations, live...); err != nil {
		return err
	}
	for _, mutation := range mutations {
		marker := s.owner.marker
		if marker == "" {
			marker = ":adoption-operation:"
		}
		if strings.Contains(mutation.Key, marker) {
			s.owner.mu.Lock()
			defer s.owner.mu.Unlock()
			if !s.owner.injected {
				s.owner.injected = true
				return errors.New("lost catalog adoption response")
			}
		}
	}
	return nil
}

func TestFleetAdoptionRecoversLostSelectionResponse(t *testing.T) {
	fleet, kv, witness := fleetTestStore(t)
	ctx := t.Context()
	provider := kv.(storage.IncarnationProvider)
	settings := identityTestSettings(filepath.Join(t.TempDir(), "state"), "", "127.0.0.1:0")
	settings.DeploymentID = fleet.identity.DeploymentID
	connected, err := openRuntime(ctx, kv, settings, runtimeCollectors{fleet: fleet})
	require.NoError(t, err)
	_, err = connected.runtime.RefreshSource(ctx)
	require.NoError(t, err)
	candidate, err := connected.CurrentCandidate(ctx)
	require.NoError(t, err)
	require.NoError(t, connected.Accept(ctx, candidate))
	require.NoError(t, connected.Close(ctx))
	original, err := fleet.CurrentPublication(ctx)
	require.NoError(t, err)
	// Keep an older input revision outside both selected heads.
	// Recovery must validate it before opening admission after an interruption.
	grant, err := fleet.AcquireLease(ctx, "fixture-publisher", time.Minute)
	require.NoError(t, err)
	next := original.Publication
	next.Expected, next.Grant = original.Head, grant
	head, err := fleet.CommitPublication(ctx, next)
	require.NoError(t, err)
	require.NoError(t, fleet.AcceptPublication(ctx, head, original.Head))
	original, err = fleet.CurrentPublication(ctx)
	require.NoError(t, err)
	history, err := fleet.AcceptedHistory(ctx)
	require.NoError(t, err)
	options, err := settings.starmapOptions()
	require.NoError(t, err)
	closed, err := witness.Close(ctx, fleet.approval)
	require.NoError(t, err)
	request := FleetAdoptionRequest{SourceApproval: fleet.approval, Closed: closed, Head: original.Head,
		BackendID: fleet.identity.BackendID, OperationID: "restore", Evidence: "isolated-fenced-fixture"}
	t.Cleanup(func() { require.NoError(t, kv.Delete(context.Background(), "recovery:authority:v1")) })

	t.Run("changed-head", func(t *testing.T) {
		changed := request
		changed.Head.Revision++
		_, err := adoptFleet(ctx, provider, witness, changed, options)
		require.Error(t, err)
		current, err := witness.Current(ctx, request.Closed.DeploymentID)
		require.NoError(t, err)
		require.Equal(t, closed, current)
	})

	var inventory fleetInventory
	data, _, err := fleet.store.ReadWithLifetime(ctx, fleet.prefix+"inventory", fleetRetentionRecordBytes)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &inventory))
	require.Len(t, inventory.Entries, 2)
	blob := inventory.Entries[0]
	require.NotEqual(t, original.Head, blob.Head, "corruption must affect retained history outside selected heads")
	chunkKey := fleet.prefix + "blob:" + blob.ID + ":" + blob.Record.Chunks[0]
	chunk, _, err := fleet.store.ReadWithLifetime(ctx, chunkKey, generationChunkSize)
	require.NoError(t, err)

	t.Run("missing-chunk-before-selection", func(t *testing.T) {
		require.NoError(t, kv.Delete(ctx, chunkKey))
		_, err := adoptFleet(ctx, provider, witness, request, options)
		require.Error(t, err)
		current, err := witness.Current(ctx, request.Closed.DeploymentID)
		require.NoError(t, err)
		require.Equal(t, closed, current)
		require.NoError(t, kv.Set(ctx, chunkKey, chunk))
	})

	t.Run("reader-resolution-requires-consent", func(t *testing.T) {
		inventory.Readers[rand.Text()] = blob.ID
		withReader, err := json.Marshal(inventory)
		require.NoError(t, err)
		require.NoError(t, kv.Set(ctx, fleet.prefix+"inventory", withReader))
		_, err = adoptFleet(ctx, provider, witness, request, options)
		require.ErrorContains(t, err, "explicit resolution")
		current, err := witness.Current(ctx, request.Closed.DeploymentID)
		require.NoError(t, err)
		require.Equal(t, closed, current)
	})
	request.ResolveReaders = true
	pending := blob
	pending.ID, pending.Head.Revision = rand.Text(), original.Head.Revision+1
	inventory.Pending = &pending
	orphan := fleet.prefix + "blob:" + pending.ID + ":" + pending.Record.Chunks[0]
	require.NoError(t, kv.Set(ctx, orphan, chunk))
	withPending, err := json.Marshal(inventory)
	require.NoError(t, err)
	require.NoError(t, kv.Set(ctx, fleet.prefix+"inventory", withPending))

	fault := &lostAdoptionReply{IncarnationProvider: provider}
	_, err = adoptFleet(ctx, fault, witness, request, options)
	require.ErrorContains(t, err, "lost catalog adoption response")
	require.True(t, fault.injected)
	_, err = kv.Get(ctx, orphan)
	require.ErrorIs(t, err, storage.ErrNotFound)
	adoptedInventory, _, err := fleet.store.ReadWithLifetime(ctx, fleet.prefix+"inventory", fleetRetentionRecordBytes)
	require.NoError(t, err)
	var adopted fleetInventory
	require.NoError(t, json.Unmarshal(adoptedInventory, &adopted))
	require.Nil(t, adopted.Pending)
	require.Empty(t, adopted.Readers)
	require.Len(t, adopted.Entries, 2)
	current, err := witness.Current(ctx, request.Closed.DeploymentID)
	require.NoError(t, err)
	require.Equal(t, closed, current, "lost selection acknowledgement must not open admission")
	_, err = NewFleetStore(ctx, provider, witness, request.Closed.DeploymentID)
	require.ErrorIs(t, err, recovery.ErrClosed)

	t.Run("corruption-during-interruption", func(t *testing.T) {
		require.NoError(t, kv.Set(ctx, chunkKey, []byte("corrupt")))
		_, err := adoptFleet(ctx, provider, witness, request, options)
		require.Error(t, err)
		current, err := witness.Current(ctx, request.Closed.DeploymentID)
		require.NoError(t, err)
		require.Equal(t, closed, current)
		require.NoError(t, kv.Set(ctx, chunkKey, chunk))
	})

	t.Run("conflicting-operation", func(t *testing.T) {
		changed := request
		changed.Evidence = "different-reconciliation"
		_, err := adoptFleet(ctx, provider, witness, changed, options)
		require.ErrorIs(t, err, recovery.ErrConflict)
	})
	approvalFault := &lostAdoptionReply{IncarnationProvider: provider, marker: "recovery:authority:v1"}
	_, err = adoptFleet(ctx, approvalFault, witness, request, options)
	require.ErrorContains(t, err, "lost catalog adoption response")
	require.True(t, approvalFault.injected)
	current, err = witness.Current(ctx, request.Closed.DeploymentID)
	require.NoError(t, err)
	require.Equal(t, closed, current, "lost native approval response must keep SQL closed")

	approved, err := adoptFleet(ctx, provider, witness, request, options)
	require.NoError(t, err)
	require.Equal(t, request.approval(), approved)
	repeated, err := adoptFleet(ctx, provider, witness, request, options)
	require.NoError(t, err)
	require.Equal(t, approved, repeated)
	restored, err := NewFleetStore(ctx, provider, witness, request.Closed.DeploymentID)
	require.NoError(t, err)
	recovered, err := restored.AcceptedPublication(ctx)
	require.NoError(t, err)
	require.Equal(t, original.Publication, recovered.Publication, "original publication and acquisition grant must remain unchanged")
	require.Equal(t, original.Head, recovered.Adoption.Previous)
	retained, err := restored.AcceptedHistory(ctx)
	require.NoError(t, err)
	require.Equal(t, history, retained)
	_, err = fleet.CurrentHead(ctx)
	require.Error(t, err, "old owner cannot adopt the new recovery approval")
	_, _, err = restored.store.ReadWithLifetime(ctx, restored.prefix+"lease", 4096)
	require.ErrorIs(t, err, storage.ErrNotFound, "recovery must not revive a copied acquisition lease")
	_, err = witness.OpenAuthority(ctx, provider, request.Closed.DeploymentID)
	require.NoError(t, err)
	grant, err = restored.AcquireLease(ctx, "recovered-publisher", time.Minute)
	require.NoError(t, err)
	require.Greater(t, grant.Epoch, original.Publication.Grant.Epoch)
	next = recovered.Publication
	next.Expected, next.Grant = recovered.Head, grant
	newHead, err := restored.CommitPublication(ctx, next)
	require.NoError(t, err)
	require.NoError(t, restored.AcceptPublication(ctx, newHead, recovered.Head))
	_, err = adoptFleet(ctx, provider, witness, request, options)
	require.NoError(t, err, "an approved retry must preserve later ordinary publication")
	actual, err := restored.CurrentHead(ctx)
	require.NoError(t, err)
	require.Equal(t, newHead, actual)
	require.NoError(t, restored.Release(ctx, grant))
	_, err = witness.Close(ctx, approved)
	require.NoError(t, err)
	_, err = adoptFleet(ctx, provider, witness, request, options)
	require.ErrorIs(t, err, recovery.ErrConflict, "an obsolete retry cannot reopen a later closed epoch")
}

var _ storage.IncarnationProvider = (*lostAdoptionReply)(nil)
