package sqlstore

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

func TestRecoveryTargetBindsNativeSQLWithoutPasswords(t *testing.T) {
	for name, config := range contractConfigs(t) {
		t.Run(name, func(t *testing.T) {
			if config.Type == TypeSQLite {
				require.NoError(t, os.Chmod(filepath.Dir(config.SQLite.Path), 0o700))
			}
			db, err := Open(config)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			require.NoError(t, db.Ping(t.Context()))
			before, err := db.RecoveryTargetSHA256(t.Context(), config)
			require.NoError(t, err)
			require.Len(t, before, 64)
			changed := config
			switch config.Type {
			case TypePostgres:
				parsed, err := url.Parse(config.Postgres.URL)
				require.NoError(t, err)
				parsed.User = url.UserPassword(parsed.User.Username(), "changed-password")
				changed.Postgres.URL = parsed.String()
			case TypeMySQL:
				parsed, err := mysql.ParseDSN(config.MySQL.DSN)
				require.NoError(t, err)
				parsed.Passwd = "changed-password"
				changed.MySQL.DSN = parsed.FormatDSN()
			}
			after, err := db.RecoveryTargetSHA256(t.Context(), changed)
			require.NoError(t, err)
			require.Equal(t, before, after)
			if config.Type == TypePostgres {
				db.SetMaxOpenConns(1)
				var original string
				require.NoError(t, db.QueryRowContext(t.Context(), "SELECT current_setting('search_path')").Scan(&original))
				_, err = db.ExecContext(t.Context(), "SET search_path TO pg_catalog")
				require.NoError(t, err)
				after, err = db.RecoveryTargetSHA256(t.Context(), config)
				require.NoError(t, err)
				require.NotEqual(t, before, after, "an unchanged URL cannot hide a changed effective schema")
				_, err = db.ExecContext(t.Context(), "SELECT set_config('search_path', $1, false)", original)
				require.NoError(t, err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			_, err = db.RecoveryTargetSHA256(ctx, config)
			require.Error(t, err)
			if config.Type == TypeSQLite {
				require.NoError(t, db.Close())
				require.NoError(t, os.Rename(config.SQLite.Path, config.SQLite.Path+"-old"))
				require.NoError(t, os.WriteFile(config.SQLite.Path, nil, 0o600))
				after, err = db.RecoveryTargetSHA256(t.Context(), config)
				require.NoError(t, err)
				require.NotEqual(t, before, after)
			}
		})
	}
}
