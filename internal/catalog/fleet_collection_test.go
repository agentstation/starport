package catalog

import (
	"fmt"
	"testing"
	"time"

	catalogstorage "github.com/agentstation/starmap/pkg/catalogs/storage"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestFleetRetentionRequiresExplicitCollection(t *testing.T) {
	fleet, _, _ := fleetTestStore(t)
	ctx := t.Context()
	grant, err := fleet.AcquireLease(ctx, "publisher", time.Minute)
	require.NoError(t, err)
	var head, first runtime.FleetHead
	for i := range catalogGenerationIndexCap + 8 {
		previous := head
		head, err = fleet.CommitPublication(ctx, fleetTestPublication(t, grant, previous, fmt.Sprintf("explicit-%d", i)))
		require.NoError(t, err)
		require.NoError(t, fleet.AcceptPublication(ctx, head, previous))
		if i == 0 {
			first = head
		}
	}
	_, err = fleet.Get(ctx, first.GenerationID)
	require.NoError(t, err, "publication and acceptance cannot bypass disabled cleanup")
	collector, ok := any(fleet).(catalogstorage.GenerationCollector)
	require.True(t, ok, "fleet must expose explicit coordinated collection")
	request := catalogstorage.RetentionRequest{ExpectedGenerationID: head.GenerationID, MaxGenerations: catalogGenerationIndexCap, MaxBytes: 1 << 30}
	dry := request
	dry.DryRun = true
	planned, err := collector.Collect(ctx, dry)
	require.NoError(t, err)
	require.Equal(t, planned.Before, planned.After)
	require.Contains(t, planned.Candidates, first.GenerationID)
	_, err = fleet.Get(ctx, first.GenerationID)
	require.NoError(t, err, "dry run must preserve the original generation")
	report, err := collector.Collect(ctx, request)
	require.NoError(t, err)
	require.Contains(t, report.Removed, first.GenerationID)
	require.Equal(t, catalogGenerationIndexCap, report.After.Generations)
	_, err = fleet.Get(ctx, first.GenerationID)
	require.ErrorIs(t, err, starmaperrors.ErrNotFound)
}

func TestFleetRetentionHonorsLimitsAndRequiredGenerations(t *testing.T) {
	fleet, kv, _ := fleetTestStore(t)
	ctx := t.Context()
	grant, err := fleet.AcquireLease(ctx, "publisher", time.Minute)
	require.NoError(t, err)
	var head runtime.FleetHead
	var ids []string
	for i := range 40 {
		head, err = fleet.CommitPublication(ctx, fleetTestPublication(t, grant, head, fmt.Sprintf("limits-%d", i)))
		require.NoError(t, err)
		ids = append(ids, head.GenerationID)
	}
	request := catalogstorage.RetentionRequest{ExpectedGenerationID: head.GenerationID, MaxGenerations: 35, MaxBytes: 1 << 30, RequiredGenerationIDs: []string{ids[0]}}
	report, err := fleet.Collect(ctx, request)
	require.NoError(t, err)
	require.Equal(t, 35, report.After.Generations)
	require.Len(t, report.Removed, 5)
	require.Equal(t, 33, report.Protected.Generations)
	require.NotContains(t, report.Removed, ids[0])
	for _, id := range report.Removed {
		_, err := fleet.Get(ctx, id)
		require.ErrorIs(t, err, starmaperrors.ErrNotFound)
	}
	required, err := fleet.Get(ctx, ids[0])
	require.NoError(t, err)
	require.Equal(t, ids[0], required.Manifest.GenerationID)
	before, err := kv.ScanWithPrefix(ctx, fleet.prefix, 10000)
	require.NoError(t, err)
	for _, tc := range []struct {
		name   string
		change func(*catalogstorage.RetentionRequest)
	}{
		{"predecessor", func(r *catalogstorage.RetentionRequest) { r.ExpectedGenerationID = "changed" }},
		{"required", func(r *catalogstorage.RetentionRequest) { r.RequiredGenerationIDs = []string{"missing"} }},
		{"scan", func(r *catalogstorage.RetentionRequest) { r.ScanEntries = 1 }},
		{"input", func(r *catalogstorage.RetentionRequest) { r.InputMaxBytes = 1; r.MaxGenerations = 32 }},
		{"invalid", func(r *catalogstorage.RetentionRequest) { r.MaxBytes = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalid := request
			tc.change(&invalid)
			_, err := fleet.Collect(ctx, invalid)
			require.Error(t, err)
			after, err := kv.ScanWithPrefix(ctx, fleet.prefix, 10000)
			require.NoError(t, err)
			require.ElementsMatch(t, before, after)
		})
	}
	request.MaxBytes = 1
	report, err = fleet.Collect(ctx, request)
	require.NoError(t, err)
	require.True(t, report.OverLimit)
	require.Equal(t, report.Protected, report.After)
	require.Equal(t, 33, report.After.Generations)
	_, err = fleet.Get(ctx, ids[0])
	require.NoError(t, err)
}

func TestFleetRetentionCountsGenerationsSeparatelyFromReceipts(t *testing.T) {
	fleet, _, _ := fleetTestStore(t)
	ctx := t.Context()
	grant, err := fleet.AcquireLease(ctx, "publisher", time.Minute)
	require.NoError(t, err)
	publication := fleetTestPublication(t, grant, runtime.FleetHead{}, "same-generation")
	var head, first runtime.FleetHead
	var recoveryBytes int64
	for i := range 40 {
		publication.Expected = head
		publication.Recovery.Data = []byte(fmt.Sprintf(`{"revision":%d}`, i))
		publication.Recovery.Checksum = payloadDigest(publication.Recovery.Data)
		recoveryBytes += int64(len(publication.Recovery.Data))
		previous := head
		head, err = fleet.CommitPublication(ctx, publication)
		require.NoError(t, err)
		require.NoError(t, fleet.AcceptPublication(ctx, head, previous))
		if i == 0 {
			first = head
		}
	}
	request := catalogstorage.RetentionRequest{ExpectedGenerationID: head.GenerationID, MaxGenerations: 1, MaxBytes: 1 << 30}
	report, err := fleet.Collect(ctx, request)
	require.NoError(t, err)
	require.Equal(t, 1, report.Before.Generations)
	require.Equal(t, report.Before, report.After)
	size, err := fleetGenerationBytes(publication.Generation)
	require.NoError(t, err)
	require.Equal(t, size, report.After.Bytes)
	require.Empty(t, report.Candidates)
	require.Empty(t, report.Removed)
	require.NotNil(t, report.Publications)
	require.Equal(t, 40, report.Publications.Before.Receipts)
	require.Equal(t, 32, report.Publications.After.Receipts)
	require.Equal(t, recoveryBytes, report.Publications.Before.RecoveryBytes)
	require.Less(t, report.Publications.After.RecoveryBytes, recoveryBytes)
	require.Greater(t, report.Publications.After.EncodedBytes, report.After.Bytes)
	_, err = fleet.Publication(ctx, first)
	require.ErrorIs(t, err, starmaperrors.ErrNotFound)
	current, err := fleet.Get(ctx, head.GenerationID)
	require.NoError(t, err)
	require.Equal(t, publication.Generation.Manifest, current.Manifest)
}

func TestFleetRetentionDryRunPreservesPendingRecovery(t *testing.T) {
	fleet, kv, _ := fleetTestStore(t)
	ctx := t.Context()
	grant, err := fleet.AcquireLease(ctx, "publisher", time.Minute)
	require.NoError(t, err)
	head, err := fleet.CommitPublication(ctx, fleetTestPublication(t, grant, runtime.FleetHead{}, "before-pending"))
	require.NoError(t, err)
	original := fleet.store
	fleet.store = &lostChunkReply{IncarnationStore: original}
	_, err = fleet.CommitPublication(ctx, fleetTestPublication(t, grant, head, "pending"))
	require.ErrorContains(t, err, "lost native response")
	fleet.store = original
	before, err := kv.ScanWithPrefix(ctx, fleet.prefix, 10000)
	require.NoError(t, err)
	request := catalogstorage.RetentionRequest{ExpectedGenerationID: head.GenerationID, MaxGenerations: 32, MaxBytes: 1 << 30, DryRun: true}
	report, err := fleet.Collect(ctx, request)
	require.NoError(t, err)
	require.Equal(t, report.Before, report.After)
	require.Equal(t, report.Publications.Before, report.Publications.After)
	require.Greater(t, report.Publications.Before.EncodedBytes, report.Publications.Projected.EncodedBytes)
	after, err := kv.ScanWithPrefix(ctx, fleet.prefix, 10000)
	require.NoError(t, err)
	require.ElementsMatch(t, before, after)
	request.DryRun = false
	report, err = fleet.Collect(ctx, request)
	require.NoError(t, err)
	require.Equal(t, report.Publications.Projected, report.Publications.After)
	current, err := fleet.CurrentHead(ctx)
	require.NoError(t, err)
	require.Equal(t, head, current)
}

func TestFleetRetentionResumesInterruptedCollection(t *testing.T) {
	fleet, kv, witness := fleetTestStore(t)
	ctx := t.Context()
	grant, err := fleet.AcquireLease(ctx, "publisher", time.Minute)
	require.NoError(t, err)
	var head runtime.FleetHead
	for i := range 40 {
		head, err = fleet.CommitPublication(ctx, fleetTestPublication(t, grant, head, fmt.Sprintf("interrupted-collection-%d", i)))
		require.NoError(t, err)
	}
	request := catalogstorage.RetentionRequest{ExpectedGenerationID: head.GenerationID, MaxGenerations: 32, MaxBytes: 1 << 30}
	fleet.store = &lostChunkReply{IncarnationStore: fleet.store, failDelete: true}
	partial, err := fleet.Collect(ctx, request)
	require.ErrorContains(t, err, "lost native response")
	require.Len(t, partial.Removed, 1)
	require.Equal(t, 39, partial.After.Generations)
	replacement, err := NewFleetStore(ctx, kv.(storage.IncarnationProvider), witness, fleet.identity.DeploymentID)
	require.NoError(t, err)
	report, err := replacement.Collect(ctx, request)
	require.NoError(t, err)
	require.Equal(t, 32, report.After.Generations)
	m, err := replacement.maintain(ctx)
	require.NoError(t, err)
	require.Nil(t, m.inventory.Pending)
	require.NoError(t, m.close())
	current, err := replacement.CurrentHead(ctx)
	require.NoError(t, err)
	require.Equal(t, head, current)
}
