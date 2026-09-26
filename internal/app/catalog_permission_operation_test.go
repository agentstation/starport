package app

import (
	"encoding/json/v2"
	"errors"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/stretchr/testify/require"
)

func TestCatalogOperationReportsPermissionAndAdmittedStreamPolicy(t *testing.T) {
	for _, name := range []string{"accepted", "withdrawal with rejected replacement", "withdrawal with failed source"} {
		withdraw := name != "accepted"
		t.Run(name, func(t *testing.T) {
			fixture := newRuntimeRefreshFixture(t)
			before := fixture.registry.Snapshot()
			if name == "withdrawal with failed source" {
				fixture.runtime.onRefresh = func() { fixture.permission.allowed.Store(false) }
				fixture.runtime.refreshFailure = errors.New("source unavailable")
			} else if withdraw {
				fixture.application.newConnector = func(string, []catalogs.EndpointType, connectors.ProviderConfig) (connectors.Connector, error) {
					fixture.permission.allowed.Store(false)
					return nil, errors.New("replacement rejected")
				}
			}
			operations := runtimecatalog.NewOperations()
			t.Cleanup(operations.Close)
			operation, _ := operations.Submit(runtimecatalog.KindCatalogUpdate, fixture.application.runCatalogUpdate)
			var completed runtimecatalog.Operation
			require.Eventually(t, func() bool {
				var err error
				completed, err = operations.Get(operation.ID)
				require.NoError(t, err)
				return !completed.Open()
			}, 5*time.Second, time.Millisecond)
			if withdraw {
				require.Equal(t, runtimecatalog.OperationFailed, completed.State)
				require.Same(t, before, fixture.registry.Snapshot())
				require.False(t, before.AllowsNewAttempt())
			} else {
				require.Equal(t, runtimecatalog.OperationSucceeded, completed.State)
			}
			encoded, err := json.Marshal(completed)
			require.NoError(t, err)
			var response map[string]any
			require.NoError(t, json.Unmarshal(encoded, &response))
			require.Equal(t, map[string]any{
				"new_attempts_allowed":        !withdraw,
				"admitted_streams_may_finish": true,
			}, response["permission_at_completion"])
			fixture.permission.allowed.Store(withdraw)
			retained, err := operations.Get(operation.ID)
			require.NoError(t, err)
			require.Equal(t, completed, retained, "operation evidence must not follow later permission changes")
		})
	}
}
