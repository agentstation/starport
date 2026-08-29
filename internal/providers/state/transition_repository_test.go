package state

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/sqlstore"
)

func openTransitionRepository(t *testing.T) TransitionRepository {
	t.Helper()
	db, err := sqlstore.Open(sqlstore.Config{
		Type:   sqlstore.TypeSQLite,
		SQLite: sqlstore.SQLiteConfig{Path: filepath.Join(t.TempDir(), "starport.db")},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Migrate(context.Background()))
	repository, err := OpenTransitions(db)
	require.NoError(t, err)
	return repository
}

func TestTransitionRepositoryRoundTripsNewestFirst(t *testing.T) {
	repository := openTransitionRepository(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)

	require.NoError(t, repository.Record(ctx, []IncidentTransition{
		{ProviderID: "openai", Indicator: "minor", Description: "Degraded latency", ObservedAt: base.Add(-2 * time.Hour)},
		{ProviderID: "openai", Indicator: "major", Description: "Elevated error rates", ObservedAt: base.Add(-time.Hour)},
		{ProviderID: "anthropic", Indicator: "critical", Description: "Outage", ObservedAt: base.Add(-time.Hour)},
	}))
	require.NoError(t, repository.Record(ctx, []IncidentTransition{
		{ProviderID: "openai", Indicator: "none", ObservedAt: base},
	}))

	transitions, err := repository.Transitions(ctx, "openai")
	require.NoError(t, err)
	require.Len(t, transitions, 3)
	require.Equal(t, "none", transitions[0].Indicator)
	require.Equal(t, "major", transitions[1].Indicator)
	require.Equal(t, "minor", transitions[2].Indicator)
	require.Equal(t, catalogs.ProviderID("openai"), transitions[0].ProviderID)
	require.Equal(t, base, transitions[0].ObservedAt)
	require.Equal(t, "Elevated error rates", transitions[1].Description)

	other, err := repository.Transitions(ctx, "anthropic")
	require.NoError(t, err)
	require.Len(t, other, 1)

	none, err := repository.Transitions(ctx, "")
	require.NoError(t, err)
	require.Empty(t, none)
}

func TestTransitionRepositoryPrunesBeyondRetention(t *testing.T) {
	repository := openTransitionRepository(t)
	ctx := context.Background()
	now := time.Now().UTC()

	require.NoError(t, repository.Record(ctx, []IncidentTransition{
		{ProviderID: "openai", Indicator: "major", ObservedAt: now.Add(-transitionRetention - time.Hour)},
		{ProviderID: "openai", Indicator: "none", ObservedAt: now},
	}))

	transitions, err := repository.Transitions(ctx, "openai")
	require.NoError(t, err)
	require.Len(t, transitions, 1)
	require.Equal(t, "none", transitions[0].Indicator)
}

func TestTransitionRepositorySkipsUnaddressableRows(t *testing.T) {
	repository := openTransitionRepository(t)
	ctx := context.Background()

	require.NoError(t, repository.Record(ctx, nil))
	require.NoError(t, repository.Record(ctx, []IncidentTransition{
		{ProviderID: "", Indicator: "major", ObservedAt: time.Now().UTC()},
	}))

	transitions, err := repository.Transitions(ctx, "openai")
	require.NoError(t, err)
	require.Empty(t, transitions)
}

func TestOpenTransitionsRefusesANilStore(t *testing.T) {
	_, err := OpenTransitions(nil)
	require.ErrorIs(t, err, ErrTransitionStoreRequired)
}
