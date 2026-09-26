package registry

import (
	"fmt"
	"sync"
	"testing"

	"github.com/agentstation/starmap"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/stretchr/testify/require"
)

func TestRuntimeGenerationCapacityPreservesLeases(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)
	registry, err := Open(plane, []Registration{runtimeRegistration("openai", newCloseTrackingConnector(), registryTestMaterialSource{})})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, registry.Close()) })
	var leases []connectors.RuntimeLease
	defer func() {
		for _, lease := range leases {
			lease.Release()
		}
	}()
	for index := range 3 {
		lease, err := registry.AcquireRuntime()
		require.NoError(t, err)
		leases = append(leases, lease)
		candidate, err := registry.Prepare([]Registration{runtimeRegistration("openai", newCloseTrackingConnector(), registryTestMaterialSource{})})
		require.NoError(t, err)
		state := client.CurrentCatalogState()
		state.GenerationID = fmt.Sprintf("capacity-%d", index)
		state.Sequence += uint64(index + 1)
		snapshot, err := plane.ReplaceRuntime(state, candidate.Availability())
		require.NoError(t, err)
		require.NoError(t, registry.Publish(candidate, snapshot))
	}
	refusedConnector := newCloseTrackingConnector()
	candidate, err := registry.Prepare([]Registration{runtimeRegistration("openai", refusedConnector, registryTestMaterialSource{})})
	if candidate != nil {
		require.NoError(t, candidate.Close())
	}
	require.ErrorIs(t, err, runtimecatalog.ErrRuntimeGenerationCapacity)
	require.Equal(t, int32(1), refusedConnector.closeCount.Load())
	for _, lease := range leases {
		require.NotNil(t, lease.Get("openai"))
	}
	leases[0].Release()
	candidate, err = registry.Prepare([]Registration{runtimeRegistration("openai", newCloseTrackingConnector(), registryTestMaterialSource{})})
	require.NoError(t, err)
	require.NoError(t, candidate.Close())
}

func TestRuntimeGenerationCapacityReservesConcurrentPreparations(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)
	registry, err := Open(plane, []Registration{runtimeRegistration("openai", newCloseTrackingConnector(), registryTestMaterialSource{})})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, registry.Close()) })
	type outcome struct {
		candidate *Candidate
		err       error
	}
	outcomes := make(chan outcome, 12)
	var workers sync.WaitGroup
	for range 12 {
		workers.Go(func() {
			candidate, err := registry.Prepare([]Registration{runtimeRegistration("openai", newCloseTrackingConnector(), registryTestMaterialSource{})})
			outcomes <- outcome{candidate, err}
		})
	}
	workers.Wait()
	close(outcomes)
	var prepared []*Candidate
	for result := range outcomes {
		if result.err != nil {
			require.ErrorIs(t, result.err, runtimecatalog.ErrRuntimeGenerationCapacity)
		} else {
			prepared = append(prepared, result.candidate)
		}
	}
	require.Len(t, prepared, 3)
	for _, candidate := range prepared {
		require.NoError(t, candidate.Close())
		require.NoError(t, candidate.Close())
	}
	_, err = registry.Prepare([]Registration{{Provider: "missing-connector"}})
	require.ErrorIs(t, err, ErrConnectorRequired)
	next, err := registry.Prepare([]Registration{runtimeRegistration("openai", newCloseTrackingConnector(), registryTestMaterialSource{})})
	require.NoError(t, err)
	require.NoError(t, next.Close())
}
