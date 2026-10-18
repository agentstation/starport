package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/sqlstore"
)

// TestRecoveryInspectionsShareTheRelationalStartupBound holds the bound that
// construction gives the two recovery inspections. They open and read the
// database that the migration opens a few steps later, so a slow disk gets
// the same time at the preflight as at the migration. A 5 s preflight bound
// failed the second dev run on a loaded Windows runner before the first
// process had finished releasing the SQLite file.
func TestRecoveryInspectionsShareTheRelationalStartupBound(t *testing.T) {
	var deadlines []time.Duration
	factories := defaultRuntimeFactories()
	factories.inspectRecoverySQL = func(ctx context.Context, _ config.StorageConfig, _ string) (sqlstore.RecoveryStartupState, error) {
		deadline, ok := ctx.Deadline()
		require.True(t, ok, "the inspection runs under a bound")
		deadlines = append(deadlines, time.Until(deadline))
		return sqlstore.RecoveryStartupState{}, nil
	}
	cfg, err := config.NewLoader().WithPaths(config.PathsForConfigDir(t.TempDir())).WithEnvFiles().WithEnvironment(map[string]string{
		"STARPORT_SECURITY_MASTER_KEY": strings.Repeat("k", 32),
	}).Load(t.Context())
	require.NoError(t, err)
	builder := runtimeBuilder{application: &App{config: cfg}, config: cfg, factories: factories}
	require.NoError(t, builder.inspectRecoveryStartup())
	require.NoError(t, builder.openStorage())
	t.Cleanup(func() { require.NoError(t, builder.application.closeLifecycle(context.Background())) })
	require.NoError(t, builder.checkRecoveryStartup())

	require.Len(t, deadlines, 2)
	for _, remaining := range deadlines {
		require.Greater(t, remaining, relationalStartupTimeout-5*time.Second, "an inspection keeps the relational startup bound")
		require.LessOrEqual(t, remaining, relationalStartupTimeout)
	}
	require.Equal(t, 30*time.Second, relationalStartupTimeout, "the inspections and the migration share one bound")
}
