package catalog

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"github.com/agentstation/starport/internal/storage"
	"strings"
	"testing"
	"time"

	catalogstorage "github.com/agentstation/starmap/pkg/catalogs/storage"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/agentstation/starmap/runtime"
	"github.com/stretchr/testify/require"
)

func TestFleetRetentionRefusesUnownedStaging(t *testing.T) {
	fleet, kv, _ := fleetTestStore(t)
	grant, err := fleet.AcquireLease(t.Context(), "retired-owner", time.Minute)
	require.NoError(t, err)
	require.NoError(t, fleet.Release(t.Context(), grant))
	before, err := kv.ScanWithPrefix(t.Context(), fleet.prefix, 10000)
	require.NoError(t, err)
	for i := range 12 {
		_, err = fleet.CommitPublication(t.Context(), fleetTestPublication(t, grant, runtime.FleetHead{}, fmt.Sprintf("rejected-%d", i)))
		require.ErrorIs(t, err, starmaperrors.ErrConflict)
	}
	after, err := kv.ScanWithPrefix(t.Context(), fleet.prefix, 10000)
	require.NoError(t, err)
	require.ElementsMatch(t, before, after, "rejected ownership must not consume persistent payload storage")
}

func TestFleetRetentionBoundsCommittedSnapshots(t *testing.T) {
	fleet, _, _ := fleetTestStore(t)
	grant, err := fleet.AcquireLease(t.Context(), "publisher", time.Minute)
	require.NoError(t, err)
	var head runtime.FleetHead
	var first runtime.FleetHead
	for i := range catalogGenerationIndexCap + 8 {
		previous := head
		head, err = fleet.CommitPublication(t.Context(), fleetTestPublication(t, grant, previous, fmt.Sprintf("retained-%d", i)))
		require.NoError(t, err)
		require.NoError(t, fleet.AcceptPublication(t.Context(), head, previous))
		if i == 0 {
			first = head
		}
	}
	collectFleetTest(t, fleet, head)
	history, err := fleet.AcceptedHistory(t.Context())
	require.NoError(t, err)
	require.Len(t, history, catalogGenerationIndexCap)
	for _, entry := range history {
		_, err := fleet.Get(t.Context(), entry.GenerationID)
		require.NoError(t, err, "retained rollback generation must remain readable")
	}
	_, err = fleet.Publication(t.Context(), first)
	require.ErrorIs(t, err, starmaperrors.ErrNotFound, "expired publication receipts must not grow without a bound")
	_, err = fleet.Get(t.Context(), first.GenerationID)
	require.ErrorIs(t, err, starmaperrors.ErrNotFound)
	current, err := fleet.CurrentPublication(t.Context())
	require.NoError(t, err)
	require.Equal(t, head, current.Head)
	accepted, err := fleet.AcceptedPublication(t.Context())
	require.NoError(t, err)
	require.Equal(t, head, accepted.Head)
}

func TestFleetRetentionProtectsPinnedGeneration(t *testing.T) {
	fleet, _, _ := fleetTestStore(t)
	ctx := t.Context()
	grant, err := fleet.AcquireLease(ctx, "publisher", 3*time.Minute)
	require.NoError(t, err)
	first, err := fleet.CommitPublication(ctx, fleetTestPublication(t, grant, runtime.FleetHead{}, "pinned"))
	require.NoError(t, err)
	require.NoError(t, fleet.AcceptPublication(ctx, first, runtime.FleetHead{}))
	generation, release, err := fleet.AcquireGeneration(ctx, first.GenerationID)
	require.NoError(t, err)
	require.Equal(t, first.GenerationID, generation.Manifest.GenerationID)
	head := first
	for i := range catalogGenerationIndexCap + 3 {
		previous := head
		head, err = fleet.CommitPublication(ctx, fleetTestPublication(t, grant, previous, fmt.Sprintf("after-pin-%d", i)))
		require.NoError(t, err)
		require.NoError(t, fleet.AcceptPublication(ctx, head, previous))
	}
	collectFleetTest(t, fleet, head)
	_, err = fleet.Get(ctx, first.GenerationID)
	require.NoError(t, err)
	require.NoError(t, release())
	require.NoError(t, release())
	require.NoError(t, fleet.AcceptPublication(ctx, head, head))
	collectFleetTest(t, fleet, head)
	_, err = fleet.Get(ctx, first.GenerationID)
	require.ErrorIs(t, err, starmaperrors.ErrNotFound)
	require.Equal(t, first.GenerationID, generation.Manifest.GenerationID, "the independent in-memory snapshot remains usable")
}

// lostChunkReply executes a real native mutation before losing its response.
type lostChunkReply struct {
	storage.IncarnationStore
	failDelete bool
	injected   bool
}

func (s *lostChunkReply) CompareAndSwap(ctx context.Context, changes []storage.CompareAndSwapMutation, live ...string) error {
	if err := s.IncarnationStore.CompareAndSwap(ctx, changes, live...); err != nil {
		return err
	}
	for _, change := range changes {
		if !s.injected && strings.Contains(change.Key, ":blob:") && (s.failDelete == (change.NewValue == nil)) {
			s.injected = true
			return errors.New("injected lost native response")
		}
	}
	return nil
}

func TestFleetRetentionRecoversInterruptedUploadAndDeletion(t *testing.T) {
	fleet, kv, witness := fleetTestStore(t)
	ctx := t.Context()
	grant, err := fleet.AcquireLease(ctx, "publisher", time.Minute)
	require.NoError(t, err)
	first, err := fleet.CommitPublication(ctx, fleetTestPublication(t, grant, runtime.FleetHead{}, "before-interruption"))
	require.NoError(t, err)
	original := fleet.store
	dropped := &lostChunkReply{IncarnationStore: original}
	fleet.store = dropped
	_, err = fleet.CommitPublication(ctx, fleetTestPublication(t, grant, first, "interrupted"))
	require.ErrorContains(t, err, "lost native response")
	require.True(t, dropped.injected)
	fleet.store = &lostChunkReply{IncarnationStore: original, failDelete: true}
	_, err = fleet.CommitPublication(ctx, fleetTestPublication(t, grant, first, "retry-after-interruption"))
	require.ErrorContains(t, err, "lost native response")
	replacement, err := NewFleetStore(ctx, kv.(storage.IncarnationProvider), witness, fleet.identity.DeploymentID)
	require.NoError(t, err)
	recovered, err := replacement.CurrentPublication(ctx)
	require.NoError(t, err)
	require.Equal(t, first, recovered.Head)
	maintenance, err := replacement.maintain(ctx)
	require.NoError(t, err)
	require.Nil(t, maintenance.inventory.Pending)
	require.Len(t, maintenance.inventory.Entries, 1)
	keys, err := kv.ScanWithPrefix(ctx, fleet.prefix+"blob:", 10000)
	require.NoError(t, err)
	require.Len(t, keys, len(maintenance.inventory.Entries[0].Record.Chunks))
	require.NoError(t, maintenance.close())
}

func TestFleetRetentionFencesExpiredMaintenance(t *testing.T) {
	fleet, kv, _ := fleetTestStore(t)
	ctx := t.Context()
	m, err := fleet.maintain(ctx)
	require.NoError(t, err)
	require.NoError(t, kv.Delete(ctx, fleet.prefix+"maintenance"))
	next, err := fleet.maintain(ctx)
	require.NoError(t, err)
	require.ErrorIs(t, m.mutate(ctx, []storage.CompareAndSwapMutation{{Key: fleet.prefix + "stale-writer", NewValue: []byte("forbidden")}}), storage.ErrConflict)
	require.NoError(t, m.close(), "an old release must not delete the new native grant")
	require.NoError(t, next.save(ctx))
	require.NoError(t, next.close())
	_, err = kv.Get(ctx, fleet.prefix+"stale-writer")
	require.ErrorIs(t, err, storage.ErrNotFound)
}

func TestFleetRetentionConcurrentReadersAndCollection(t *testing.T) {
	fleet, kv, witness := fleetTestStore(t)
	ctx := t.Context()
	grant, err := fleet.AcquireLease(ctx, "publisher", 3*time.Minute)
	require.NoError(t, err)
	head, err := fleet.CommitPublication(ctx, fleetTestPublication(t, grant, runtime.FleetHead{}, "concurrent-first"))
	require.NoError(t, err)
	require.NoError(t, fleet.AcceptPublication(ctx, head, runtime.FleetHead{}))
	reader, err := NewFleetStore(ctx, kv.(storage.IncarnationProvider), witness, fleet.identity.DeploymentID)
	require.NoError(t, err)
	pinned, release, err := reader.AcquireGeneration(ctx, head.GenerationID)
	require.NoError(t, err)
	defer func() { require.NoError(t, release()) }()
	done := make(chan error, 1)
	go func() {
		for range 70 {
			if _, err := reader.CurrentPublication(ctx); err != nil {
				done <- err
				return
			}
			if _, err := reader.AcceptedPublication(ctx); err != nil {
				done <- err
				return
			}
			got, err := reader.Get(ctx, pinned.Manifest.GenerationID)
			if err != nil {
				done <- err
				return
			}
			if got.Manifest.Payload.Checksum != pinned.Manifest.Payload.Checksum {
				done <- errors.New("reader content changed")
				return
			}
		}
		done <- nil
	}()
	for i := range 70 {
		previous := head
		head, err = fleet.CommitPublication(ctx, fleetTestPublication(t, grant, previous, fmt.Sprintf("concurrent-%d", i)))
		require.NoError(t, err)
		require.NoError(t, fleet.AcceptPublication(ctx, head, previous))
		collectFleetTest(t, fleet, head)
	}
	require.NoError(t, <-done)
}

func TestFleetRetentionCapacityRefusesWithoutMovingHead(t *testing.T) {
	fleet, kv, _ := fleetTestStore(t)
	ctx := t.Context()
	grant, err := fleet.AcquireLease(ctx, "publisher", 3*time.Minute)
	require.NoError(t, err)
	var head runtime.FleetHead
	releases := make([]func() error, 0, fleetRetentionMaxEntries)
	defer func() {
		for _, release := range releases {
			require.NoError(t, release())
		}
	}()
	for i := range fleetRetentionMaxEntries {
		previous := head
		head, err = fleet.CommitPublication(ctx, fleetTestPublication(t, grant, previous, fmt.Sprintf("protected-%d", i)))
		require.NoError(t, err)
		require.NoError(t, fleet.AcceptPublication(ctx, head, previous))
		_, release, err := fleet.AcquireGeneration(ctx, head.GenerationID)
		require.NoError(t, err)
		releases = append(releases, release)
	}
	before, err := kv.ScanWithPrefix(ctx, fleet.prefix, 10000)
	require.NoError(t, err)
	_, err = fleet.CommitPublication(ctx, fleetTestPublication(t, grant, head, "over-capacity"))
	require.ErrorContains(t, err, "retention capacity reached")
	after, err := kv.ScanWithPrefix(ctx, fleet.prefix, 10000)
	require.NoError(t, err)
	require.ElementsMatch(t, before, after)
	current, err := fleet.CurrentHead(ctx)
	require.NoError(t, err)
	require.Equal(t, head, current)
	require.NoError(t, releases[0]())
	collectFleetTest(t, fleet, head)
	_, err = fleet.CommitPublication(ctx, fleetTestPublication(t, grant, head, "capacity-recovered"))
	require.NoError(t, err)
}

func TestFleetRetentionRejectsOverlappingDeletionRecord(t *testing.T) {
	fleet, kv, _ := fleetTestStore(t)
	ctx := t.Context()
	grant, err := fleet.AcquireLease(ctx, "publisher", time.Minute)
	require.NoError(t, err)
	head, err := fleet.CommitPublication(ctx, fleetTestPublication(t, grant, runtime.FleetHead{}, "protected-current"))
	require.NoError(t, err)
	m, err := fleet.maintain(ctx)
	require.NoError(t, err)
	m.inventory.Pending = &m.inventory.Entries[0]
	corrupt, err := json.Marshal(m.inventory)
	require.NoError(t, err)
	require.NoError(t, kv.Set(ctx, fleet.prefix+"inventory", corrupt))
	require.NoError(t, m.close())
	before, err := kv.ScanWithPrefix(ctx, fleet.prefix+"blob:", 10000)
	require.NoError(t, err)
	_, err = fleet.CommitPublication(ctx, fleetTestPublication(t, grant, head, "must-refuse"))
	require.ErrorContains(t, err, "overlaps")
	after, err := kv.ScanWithPrefix(ctx, fleet.prefix+"blob:", 10000)
	require.NoError(t, err)
	require.ElementsMatch(t, before, after)
}

func TestFleetRetentionCorruptionCannotDeleteSelectedBytes(t *testing.T) {
	for _, mode := range []string{"current", "accepted", "rollback", "inventory-missing"} {
		t.Run(mode, func(t *testing.T) {
			fleet, kv, _ := fleetTestStore(t)
			ctx := t.Context()
			grant, err := fleet.AcquireLease(ctx, "publisher", time.Minute)
			require.NoError(t, err)
			first, err := fleet.CommitPublication(ctx, fleetTestPublication(t, grant, runtime.FleetHead{}, "protected"))
			require.NoError(t, err)
			require.NoError(t, fleet.AcceptPublication(ctx, first, runtime.FleetHead{}))
			if mode == "rollback" {
				head, err := fleet.CommitPublication(ctx, fleetTestPublication(t, grant, first, "newer"))
				require.NoError(t, err)
				require.NoError(t, fleet.AcceptPublication(ctx, head, first))
			}
			m, err := fleet.maintain(ctx)
			require.NoError(t, err)
			if mode == "inventory-missing" {
				require.NoError(t, kv.Delete(ctx, fleet.prefix+"inventory"))
			} else {
				entry := m.inventory.Entries[0]
				m.inventory.Entries = m.inventory.Entries[1:]
				m.inventory.Pending = &entry
				corrupt, err := json.Marshal(m.inventory)
				require.NoError(t, err)
				require.NoError(t, kv.Set(ctx, fleet.prefix+"inventory", corrupt))
				if mode == "accepted" {
					require.NoError(t, kv.Delete(ctx, fleet.prefix+"head"))
				}
			}
			require.NoError(t, m.close())
			before, err := kv.ScanWithPrefix(ctx, fleet.prefix+"blob:", 10000)
			require.NoError(t, err)
			_, err = fleet.maintain(ctx)
			require.Error(t, err)
			after, err := kv.ScanWithPrefix(ctx, fleet.prefix+"blob:", 10000)
			require.NoError(t, err)
			require.ElementsMatch(t, before, after)
		})
	}
}

func collectFleetTest(t *testing.T, fleet *FleetStore, head runtime.FleetHead) catalogstorage.RetentionReport {
	t.Helper()
	report, err := fleet.Collect(t.Context(), catalogstorage.RetentionRequest{ExpectedGenerationID: head.GenerationID, MaxGenerations: catalogGenerationIndexCap, MaxBytes: 1 << 30})
	require.NoError(t, err)
	return report
}
