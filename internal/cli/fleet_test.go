package cli

import (
	"context"
	"errors"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/config"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestFleetInitializationCommandIsDiscoverable(t *testing.T) {
	deps, output, _ := testDependencies()
	require.NoError(t, Run(t.Context(), []string{"starport", "fleet", "init", "--help"}, deps))
	require.Contains(t, output.String(), "--operation")
	require.Contains(t, output.String(), "--evidence")
}

func TestFleetInitializationCommand(t *testing.T) {
	for _, mode := range []string{"success", "missing-operation", "empty-evidence", "extra-argument", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			deps, output, _ := testDependencies()
			calls := 0
			failure := errors.New("backend unavailable")
			deps.InitializeFleet = func(_ context.Context, cfg *config.Config, request recovery.FreshRequest) (recovery.Record, error) {
				calls++
				require.NotNil(t, cfg)
				require.Equal(t, recovery.FreshRequest{OperationID: "first", Evidence: "ticket-123"}, request)
				if mode == "unavailable" {
					return recovery.Record{}, failure
				}
				return recovery.Record{DeploymentID: "deployment", Epoch: 1, Open: true}, nil
			}
			args := []string{"starport", "fleet", "init", "--operation", "first", "--evidence", "ticket-123", "--json"}
			switch mode {
			case "missing-operation":
				args = []string{"starport", "fleet", "init", "--evidence", "ticket-123"}
			case "empty-evidence":
				args[6] = " "
			case "extra-argument":
				args = append(args, "extra")
			}
			err := Run(t.Context(), args, deps)
			switch mode {
			case "success":
				require.NoError(t, err)
				require.Equal(t, 1, calls)
				require.Contains(t, output.String(), `"DeploymentID": "deployment"`)
			case "unavailable":
				require.ErrorIs(t, err, failure)
				require.Equal(t, ExitCodeRuntime, ExitCode(err))
				require.Empty(t, output.String())
			default:
				require.Error(t, err)
				require.Zero(t, calls)
				require.Equal(t, ExitCodeUsage, ExitCode(err))
			}
		})
	}
}
