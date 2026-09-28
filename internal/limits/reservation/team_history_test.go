package reservation

import (
	"context"
	"crypto/rand"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestTeamBudgetHistoryHandoff(t *testing.T) {
	for _, profile := range []struct{ sql, kv string }{{"sqlite", "badger"}, {"postgres", "valkey"}} {
		t.Run(profile.sql+"-"+profile.kv, func(t *testing.T) {
			db := teamHistorySQL(t, profile.sql)
			repositories, err := identity.Open(db)
			require.NoError(t, err)
			authority := repositories.Teams.(identity.TeamBudgetHistory)
			for _, mode := range []string{"normal", "lost-ack", "lost-sql-ack", "before-write", "lost-state", "recreated", "added-budget", "changed-policy", "wrong-interval", "concurrent"} {
				t.Run(mode, func(t *testing.T) {
					f := openFixture(t, profile.kv)
					team := identity.Team{ID: "team-" + rand.Text(), Name: "Budget team", Budget: &limits.TeamBudget{Limit: 1000, Interval: limits.IntervalDay}}
					if mode == "added-budget" {
						team.Budget = nil
					}
					created, err := repositories.Teams.Create(t.Context(), team)
					require.NoError(t, err)
					if mode == "added-budget" {
						created.Team.Budget = &limits.TeamBudget{Limit: 1000, Interval: limits.IntervalDay}
						created, err = repositories.Teams.Update(t.Context(), created.Team, created.Revision)
						require.NoError(t, err)
					}
					initialize := func(repository *Repository, value identity.Team) error {
						return repository.InitializeTeamHistory(t.Context(), authority, value.ID, value.Budget.Interval, value.Budget.HistoryID)
					}
					switch mode {
					case "lost-sql-ack":
						lost := teamHistoryClaimFault{TeamHistoryAuthority: authority}
						err := f.repository.InitializeTeamHistory(t.Context(), lost, created.Team.ID, created.Team.Budget.Interval, created.Team.Budget.HistoryID)
						require.ErrorContains(t, err, "injected SQL acknowledgement loss")
						require.ErrorIs(t, initialize(f.repository, created.Team), ErrHistoryUnknown)
						return
					case "changed-policy":
						previous := created.Team
						created.Team.Budget = &limits.TeamBudget{Limit: 1000, Interval: limits.IntervalMonth}
						created, err = repositories.Teams.Update(t.Context(), created.Team, created.Revision)
						require.NoError(t, err)
						require.ErrorIs(t, initialize(f.repository, previous), ErrHistoryUnknown, "old policy cannot consume the unused grant")
						require.ErrorIs(t, initialize(f.repository, created.Team), ErrHistoryUnknown, "interval changes need reconciled history")
						return
					case "added-budget":
						require.ErrorIs(t, initialize(f.repository, created.Team), ErrHistoryUnknown)
						return
					case "before-write", "lost-ack":
						fault := &teamHistoryWriteFault{TimeBoundStore: f.store, after: mode == "lost-ack"}
						interrupted, err := Open(fault)
						require.NoError(t, err)
						err = initialize(interrupted, created.Team)
						if mode == "before-write" {
							require.ErrorIs(t, err, ErrHistoryUnknown)
							require.ErrorIs(t, initialize(f.repository, created.Team), ErrHistoryUnknown, "consumed SQL grant cannot retry an unproved write")
							_, _, err = f.repository.readHistory(t.Context(), teamMeter(created.Team), created.Team.Budget.HistoryID)
							require.ErrorIs(t, err, ErrHistoryUnknown)
							return
						}
						require.NoError(t, err, "native receipt proves committed initialization despite lost acknowledgement")
					case "wrong-interval":
						require.ErrorIs(t, f.repository.InitializeTeamHistory(t.Context(), authority, created.Team.ID, limits.IntervalMonth, created.Team.Budget.HistoryID), ErrHistoryUnknown)
					case "concurrent":
						var wg sync.WaitGroup
						results := make(chan error, 8)
						for range 8 {
							wg.Go(func() { results <- initialize(f.repository, created.Team) })
						}
						wg.Wait()
						close(results)
						for err := range results {
							if err != nil {
								require.ErrorIs(t, err, ErrHistoryUnknown, "a follower may observe the handoff before native acknowledgement")
							}
						}
					}
					require.NoError(t, initialize(f.repository, created.Team))
					attempt := attemptFixture()
					attempt.TeamID = created.Team.ID
					attempt.Rules = []Rule{{Meter: teamMeter(created.Team), Limit: 1000, PolicyRevision: "1", HistoryID: created.Team.Budget.HistoryID}}
					_, err = f.repository.Reserve(t.Context(), attempt)
					require.NoError(t, err)
					require.NoError(t, f.repository.Begin(t.Context(), attempt.ID))
					require.NoError(t, f.repository.Reconcile(t.Context(), attempt.ID, Evidence{ID: "measured", Quantities: Quantities{"output": 600}, Tokens: 600}))
					if mode == "lost-state" {
						receipt := teamHistoryReceipt{Version: recordVersion, Meter: teamMeter(created.Team), History: created.Team.Budget.HistoryID}
						require.NoError(t, f.raw.Delete(t.Context(), storageKey("history", receipt.Meter)))
						require.ErrorIs(t, initialize(f.repository, created.Team), ErrHistoryUnknown, "retained receipt cannot repair lost accounting")
						require.NoError(t, f.raw.Delete(t.Context(), storageKey("team-origin", receipt)))
						require.NoError(t, f.raw.Delete(t.Context(), storageKey("holder", holderIdentity{Scope: limits.ScopeTeam, ID: created.Team.ID})))
						require.ErrorIs(t, initialize(f.repository, created.Team), ErrHistoryUnknown, "independent SQL grant remains consumed after KV evidence disappears")
						return
					}
					if mode == "recreated" {
						require.NoError(t, repositories.Teams.Delete(t.Context(), created.Team.ID, created.Revision))
						recreated, err := repositories.Teams.Create(t.Context(), created.Team)
						require.NoError(t, err)
						require.NotEqual(t, created.Team.Budget.HistoryID, recreated.Team.Budget.HistoryID)
						require.ErrorIs(t, initialize(f.repository, recreated.Team), ErrHistoryUnknown)
					}
					// A new repository instance can acknowledge the original operation
					// without consuming permission or resetting the used capacity.
					reopened, err := Open(f.store)
					require.NoError(t, err)
					require.NoError(t, initialize(reopened, created.Team))
					attempt.ID = rand.Text()
					_, err = reopened.Reserve(t.Context(), attempt)
					require.ErrorIs(t, err, ErrExhausted)
				})
			}
		})
	}
}

func teamMeter(team identity.Team) Meter {
	return Meter{Scope: limits.ScopeTeam, Holder: team.ID, Dimension: limits.DimensionSpend, Interval: team.Budget.Interval}
}

type teamHistoryWriteFault struct {
	storage.TimeBoundStore
	after bool
}

type teamHistoryClaimFault struct{ TeamHistoryAuthority }

func (f teamHistoryClaimFault) ClaimBudgetHistory(ctx context.Context, id, interval, history string) (bool, error) {
	if _, err := f.TeamHistoryAuthority.ClaimBudgetHistory(ctx, id, interval, history); err != nil {
		return false, err
	}
	return false, errors.New("injected SQL acknowledgement loss")
}

func (f *teamHistoryWriteFault) CompareAndSwapInWindow(ctx context.Context, mutations []storage.CompareAndSwapMutation, window storage.TimeWindow) error {
	if f.after {
		if err := f.TimeBoundStore.CompareAndSwapInWindow(ctx, mutations, window); err != nil {
			return err
		}
	}
	return errors.New("injected handoff interruption")
}

func teamHistorySQL(t *testing.T, backend string) *sqlstore.DB {
	t.Helper()
	config := sqlstore.Config{Type: sqlstore.TypeSQLite, SQLite: sqlstore.SQLiteConfig{Path: filepath.Join(t.TempDir(), "team.db")}}
	if backend == "postgres" {
		address := os.Getenv("TEST_POSTGRES_URL")
		if address == "" {
			t.Skip("UNVERIFIED: TEST_POSTGRES_URL is not set")
		}
		config = sqlstore.Config{Type: sqlstore.TypePostgres, Postgres: sqlstore.PostgresConfig{URL: address}}
		admin, err := sqlstore.Open(config)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, admin.Close()) })
		schema := "budget_team_" + strings.ToLower(rand.Text())
		_, err = admin.ExecContext(t.Context(), `CREATE SCHEMA "`+schema+`"`)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, err := admin.ExecContext(context.Background(), `DROP SCHEMA "`+schema+`" CASCADE`)
			require.NoError(t, err)
		})
		parsed, err := url.Parse(address)
		require.NoError(t, err)
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		config.Postgres.URL = parsed.String()
	}
	db, err := sqlstore.Open(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.Migrate(t.Context()))
	return db
}
