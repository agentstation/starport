package audit

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/sqlstore"
)

func openAuditRepository(t *testing.T, retention time.Duration) *Repository {
	t.Helper()
	db, err := sqlstore.Open(sqlstore.Config{
		Type:   sqlstore.TypeSQLite,
		SQLite: sqlstore.SQLiteConfig{Path: filepath.Join(t.TempDir(), "starport.db")},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Migrate(context.Background()))
	repository, err := Open(db, retention)
	require.NoError(t, err)
	return repository
}

func TestRepositoryRoundTripsNewestFirst(t *testing.T) {
	repository := openAuditRepository(t, 0)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)

	require.NoError(t, repository.Record(ctx, Record{
		Time: base.Add(-2 * time.Hour), Actor: "key:ci",
		Action: "key.create", Subject: "deploy-key", Outcome: OutcomeOK,
	}))
	require.NoError(t, repository.Record(ctx, Record{
		Time: base.Add(-time.Hour), Actor: "console:local-token",
		Action: "key.delete", Subject: "deploy-key", Outcome: OutcomeOK,
	}))
	require.NoError(t, repository.Record(ctx, Record{
		Time: base, Actor: "user:auth0|abc",
		Action: "auth_mode.update", Subject: "disabled", Outcome: OutcomeError,
	}))

	page, err := repository.List(ctx, Query{})
	require.NoError(t, err)
	require.Len(t, page.Records, 3)
	require.Empty(t, page.NextCursor)
	require.Equal(t, "auth_mode.update", page.Records[0].Action)
	require.Equal(t, "key.delete", page.Records[1].Action)
	require.Equal(t, "key.create", page.Records[2].Action)
	require.Equal(t, "user:auth0|abc", page.Records[0].Actor)
	require.Equal(t, OutcomeError, page.Records[0].Outcome)
	require.Equal(t, base, page.Records[0].Time)
}

// TestAuditRecordCarriesRequestID pins the join to the usage listing: the
// request that carried a mutation comes back with its record, and a write
// without one stays empty rather than inventing an identifier.
func TestAuditRecordCarriesRequestID(t *testing.T) {
	repository := openAuditRepository(t, 0)
	ctx := context.Background()

	require.NoError(t, repository.Record(ctx, Record{
		Actor: "key:ci", Action: "key.create", Subject: "a", Outcome: OutcomeOK,
		RequestID: "req-42",
	}))
	require.NoError(t, repository.Record(ctx, Record{
		Actor: "key:ci", Action: "key.delete", Subject: "a", Outcome: OutcomeOK,
	}))

	page, err := repository.List(ctx, Query{})
	require.NoError(t, err)
	require.Len(t, page.Records, 2)
	require.Equal(t, "", page.Records[0].RequestID)
	require.Equal(t, "req-42", page.Records[1].RequestID)
}

func TestRepositoryFiltersByActionActorAndWindow(t *testing.T) {
	repository := openAuditRepository(t, 0)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)

	for hour, record := range []Record{
		{Actor: "key:ci", Action: "key.create", Subject: "a", Outcome: OutcomeOK},
		{Actor: "key:ci", Action: "account.create", Subject: "b", Outcome: OutcomeOK},
		{Actor: "console:ticket", Action: "key.create", Subject: "c", Outcome: OutcomeOK},
	} {
		record.Time = base.Add(time.Duration(hour) * time.Hour)
		require.NoError(t, repository.Record(ctx, record))
	}

	byAction, err := repository.List(ctx, Query{Action: "key.create"})
	require.NoError(t, err)
	require.Len(t, byAction.Records, 2)

	byActor, err := repository.List(ctx, Query{Actor: "console:ticket"})
	require.NoError(t, err)
	require.Len(t, byActor.Records, 1)
	require.Equal(t, "c", byActor.Records[0].Subject)

	window, err := repository.List(ctx, Query{
		Since: base.Add(30 * time.Minute), Until: base.Add(90 * time.Minute),
	})
	require.NoError(t, err)
	require.Len(t, window.Records, 1)
	require.Equal(t, "account.create", window.Records[0].Action)
}

func TestRepositoryPagesThroughTheCursor(t *testing.T) {
	repository := openAuditRepository(t, 0)
	ctx := context.Background()

	for index := range 5 {
		require.NoError(t, repository.Record(ctx, Record{
			Actor: "key:ci", Action: "preset.update",
			Subject: string(rune('a' + index)), Outcome: OutcomeOK,
		}))
	}

	first, err := repository.List(ctx, Query{Limit: 2})
	require.NoError(t, err)
	require.Len(t, first.Records, 2)
	require.NotEmpty(t, first.NextCursor)
	require.Equal(t, "e", first.Records[0].Subject)

	second, err := repository.List(ctx, Query{Limit: 2, Cursor: first.NextCursor})
	require.NoError(t, err)
	require.Len(t, second.Records, 2)
	require.Equal(t, "c", second.Records[0].Subject)

	last, err := repository.List(ctx, Query{Limit: 2, Cursor: second.NextCursor})
	require.NoError(t, err)
	require.Len(t, last.Records, 1)
	require.Empty(t, last.NextCursor)
	require.Equal(t, "a", last.Records[0].Subject)

	_, err = repository.List(ctx, Query{Cursor: "not-a-cursor"})
	require.ErrorIs(t, err, ErrInvalidQuery)
}

func TestRepositoryPrunesPastTheRetentionWindow(t *testing.T) {
	repository := openAuditRepository(t, 24*time.Hour)
	ctx := context.Background()
	base := time.Now().UTC()

	require.NoError(t, repository.Record(ctx, Record{
		Time: base.Add(-48 * time.Hour), Actor: "key:ci",
		Action: "key.create", Subject: "old", Outcome: OutcomeOK,
	}))
	require.NoError(t, repository.Record(ctx, Record{
		Actor: "key:ci", Action: "key.create", Subject: "fresh", Outcome: OutcomeOK,
	}))

	page, err := repository.List(ctx, Query{})
	require.NoError(t, err)
	require.Len(t, page.Records, 1)
	require.Equal(t, "fresh", page.Records[0].Subject)
}
