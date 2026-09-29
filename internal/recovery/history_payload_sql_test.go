package recovery

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

func historySQLConfig(t *testing.T, backend string) sqlstore.Config {
	t.Helper()
	if backend == sqlstore.TypeSQLite {
		return sqlstore.Config{Type: backend, SQLite: sqlstore.SQLiteConfig{Path: filepath.Join(privateKVDirectory(t), "target.db")}}
	}
	if backend == sqlstore.TypePostgres {
		address := os.Getenv("TEST_POSTGRES_URL")
		if address == "" {
			t.Skip("UNVERIFIED: TEST_POSTGRES_URL is not set")
		}
		return isolatedWitnessPostgres(t, sqlstore.Config{Type: backend, Postgres: sqlstore.PostgresConfig{URL: address}})
	}
	address := os.Getenv("TEST_MYSQL_DSN")
	if address == "" {
		t.Skip("UNVERIFIED: TEST_MYSQL_DSN is not set")
	}
	parsed, err := mysql.ParseDSN(address)
	require.NoError(t, err)
	admin, err := sqlstore.Open(sqlstore.Config{Type: backend, MySQL: sqlstore.MySQLConfig{DSN: address}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })
	name := "history_payload_" + strings.ToLower(rand.Text())
	_, err = admin.ExecContext(t.Context(), "CREATE DATABASE `"+name+"`")
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := admin.ExecContext(ctx, "DROP DATABASE `"+name+"`")
		require.NoError(t, err)
	})
	parsed.DBName = name
	return sqlstore.Config{Type: backend, MySQL: sqlstore.MySQLConfig{DSN: parsed.FormatDSN()}}
}
func TestHistoryPayloadSQLNativeWithdrawalAndAuthority(t *testing.T) {
	for _, backend := range []string{sqlstore.TypeSQLite, sqlstore.TypePostgres, sqlstore.TypeMySQL} {
		t.Run(backend, func(t *testing.T) {
			source, err := sqlstore.Open(historySQLConfig(t, sqlstore.TypeSQLite))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, source.Close()) })
			require.NoError(t, source.Migrate(t.Context()))
			people, err := identity.Open(source)
			require.NoError(t, err)
			_, err = people.Users.Create(t.Context(), identity.User{ID: "person", Subject: "private-subject"})
			require.NoError(t, err)
			grant := identity.AccountGrant{UserID: "person", AccountID: "tenant", CreatedAt: time.Now().UTC()}
			first, err := identity.NewRecoveryTransition([]identity.RecoveryIdentityEvent{{Grant: &identity.RecoveryGrantChange{After: &grant}}})
			require.NoError(t, err)
			root := privateKVDirectory(t)
			snapshot, err := source.SnapshotRelational(t.Context(), filepath.Join(root, "source"))
			require.NoError(t, err)
			target, err := sqlstore.Open(historySQLConfig(t, backend))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, target.Close()) })
			require.NoError(t, target.Migrate(t.Context()))
			imported := sqlstore.RelationalImportIdentity{OperationID: "typed-history", RestrictionID: "closed"}
			require.NoError(t, target.ImportRelationalOnce(t.Context(), filepath.Join(root, "source", "starport.db"), snapshot.Snapshot, root, imported, func(context.Context, *sql.Conn) error { return nil }))
			accepted := revision.RecoveryAuthority{RecoveryID: "independently-accepted", Epoch: "fresh-permission-epoch"}
			prepared, err := prepareHistorySQL("sql_identity", historyPayloadJSON(t, first), accepted)
			require.NoError(t, err)
			step := sqlstore.RelationalReplayStep{Sequence: 1, EvidenceSHA256: strings.Repeat("a", 64), TransitionSHA256: prepared.digest}
			firstReceipt, err := target.ReplayRelationalImport(t.Context(), snapshot.Snapshot, imported, step, func(ctx context.Context, conn *sql.Conn) error { return prepared.apply(ctx, target, conn) })
			require.NoError(t, err)
			withdrawal, err := identity.NewRecoveryTransition([]identity.RecoveryIdentityEvent{{Grant: &identity.RecoveryGrantChange{Before: &grant}}})
			require.NoError(t, err)
			prepared, err = prepareHistorySQL("sql_identity", historyPayloadJSON(t, withdrawal), accepted)
			require.NoError(t, err)
			second := sqlstore.RelationalReplayStep{Sequence: 2, PreviousSHA256: firstReceipt, EvidenceSHA256: strings.Repeat("b", 64), TransitionSHA256: prepared.digest}
			interrupted := errors.New("interrupted replay")
			_, err = target.ReplayRelationalImport(t.Context(), snapshot.Snapshot, imported, second, func(ctx context.Context, conn *sql.Conn) error {
				require.NoError(t, prepared.apply(ctx, target, conn))
				return interrupted
			})
			require.ErrorIs(t, err, interrupted)
			receipts, err := identity.Open(target)
			require.NoError(t, err)
			remaining, err := receipts.AccountGrants.ReachableAccounts(t.Context(), "person")
			require.NoError(t, err)
			require.Equal(t, []string{"tenant"}, remaining)
			last, err := target.ReplayRelationalImport(t.Context(), snapshot.Snapshot, imported, second, func(ctx context.Context, conn *sql.Conn) error { return prepared.apply(ctx, target, conn) })
			require.NoError(t, err)
			repeated, err := target.ReplayRelationalImport(t.Context(), snapshot.Snapshot, imported, step, func(context.Context, *sql.Conn) error { return errors.New("old callback must not execute") })
			require.NoError(t, err)
			require.Equal(t, firstReceipt, repeated)
			remaining, err = receipts.AccountGrants.ReachableAccounts(t.Context(), "person")
			require.NoError(t, err)
			require.Empty(t, remaining)
			conn, err := target.Conn(t.Context())
			require.NoError(t, err)
			stamp, err := revision.CaptureSQLRecovery(t.Context(), target, conn)
			require.NoError(t, err)
			require.NoError(t, conn.Close())
			rotation := historySQLAuthorityPayload{Version: 1, Expected: stamp}
			prepared, err = prepareHistorySQL("sql_authorization_final", historyPayloadJSON(t, rotation), accepted)
			require.NoError(t, err)
			final := sqlstore.RelationalReplayStep{Sequence: 3, PreviousSHA256: last, EvidenceSHA256: strings.Repeat("c", 64), TransitionSHA256: prepared.digest}

			_, err = target.ReplayRelationalImport(t.Context(), snapshot.Snapshot, imported, final, func(ctx context.Context, conn *sql.Conn) error { return prepared.apply(ctx, target, conn) })
			require.NoError(t, err)
			require.ErrorIs(t, target.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
		})
	}
}
func TestHistoryPayloadSQLRefusesImplicitOrUnsupportedInput(t *testing.T) {
	for _, data := range []string{`{"version":1,"events":[{"grant":{"after":{"account_id":"tenant","user_id":"person","created_at":"2026-09-29T00:00:00Z"}}}]}`, `{"version":1,"sql":"DELETE FROM users"}`, `{"version":2,"events":[]}`} {
		_, err := prepareHistorySQL("sql_identity", []byte(data), revision.RecoveryAuthority{})
		require.Error(t, err)
	}
	transition, err := revision.NewSQLRecoveryTransition(nil, revision.RecoveryAuthority{RecoveryID: "accepted", Epoch: "new"})
	require.NoError(t, err)
	data, err := json.Marshal(transition)
	require.NoError(t, err)
	_, err = prepareHistorySQL("sql_authorization_final", data, revision.RecoveryAuthority{})
	require.Error(t, err)
	_, err = prepareHistorySQL("raw_sql", data, revision.RecoveryAuthority{})
	require.Error(t, err)
}
