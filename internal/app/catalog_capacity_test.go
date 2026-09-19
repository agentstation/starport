package app

import (
	"fmt"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/stretchr/testify/require"
)

func TestCatalogCapacityRefusesBeforeAcceptance(t *testing.T) {
	for _, withdraw := range []bool{false, true} {
		t.Run(fmt.Sprintf("withdraw-%t", withdraw), func(t *testing.T) {
			fixture := newRuntimeRefreshFixture(t)
			fixture.application.newConnector = func(string, []catalogs.EndpointType, connectors.ProviderConfig) (connectors.Connector, error) {
				return &runtimeRefreshConnector{Connector: connectors.NewMockConnector(connectors.ProviderConfig{})}, nil
			}
			var leases []connectors.RuntimeLease
			defer func() {
				for _, lease := range leases {
					lease.Release()
				}
			}()
			for index := range 3 {
				lease, err := fixture.registry.AcquireRuntime()
				require.NoError(t, err)
				leases = append(leases, lease)
				fixture.runtime.state.GenerationID = fmt.Sprintf("accepted-%d", index)
				fixture.runtime.state.Sequence++
				_, err = fixture.application.runCatalogUpdate(t.Context())
				require.NoError(t, err)
			}
			before := fixture.registry.Snapshot()
			fixture.runtime.state.GenerationID = "refused-capacity"
			fixture.runtime.state.Sequence++
			fixture.permission.allowed.Store(!withdraw)
			result, err := fixture.application.runCatalogUpdate(t.Context())
			require.ErrorIs(t, err, runtimecatalog.ErrRuntimeGenerationCapacity)
			require.Equal(t, runtimecatalog.ReasonRuntimeGenerationCapacity, runtimecatalog.ClassifyOperationFailure(err))
			require.Equal(t, int32(3), fixture.runtime.accepted.Load(), "capacity refusal must precede durable acceptance")
			require.Same(t, before, fixture.registry.Snapshot())
			require.Equal(t, !withdraw, result.PermissionAtCompletion.NewAttemptsAllowed)
			require.True(t, result.PermissionAtCompletion.AdmittedStreamsMayFinish)
			for _, lease := range leases {
				require.NotNil(t, lease.Get("openai"))
				require.Equal(t, int32(0), lease.Get("openai").(*runtimeRefreshConnector).closed.Load())
			}
			leases[0].Release()
			fixture.permission.allowed.Store(true)
			_, err = fixture.application.runCatalogUpdate(t.Context())
			require.NoError(t, err)
			require.Equal(t, int32(4), fixture.runtime.accepted.Load())
			require.Equal(t, "refused-capacity", fixture.registry.Snapshot().GenerationID())
		})
	}
}
