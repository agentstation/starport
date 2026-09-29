package app

import (
	"context"
	"testing"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/stretchr/testify/require"
)

func TestStartupRefusesPendingRelationalImport(t *testing.T) {
	db, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypeSQLite})
	require.NoError(t, err)
	require.NoError(t, db.Migrate(t.Context()))
	// Any retained import marker must stop startup before concept repositories open.
	_, err = db.ExecContext(t.Context(), "INSERT INTO sqlstore_meta(name,value) VALUES('relational-import-v1','pending')")
	require.NoError(t, err)
	application := &App{}
	builder := &runtimeBuilder{application: application, config: validProductionConfig(t), factories: defaultRuntimeFactories()}
	builder.factories.openSQL = func(config.StorageConfig) (*sqlstore.DB, error) { return db, nil }
	t.Cleanup(func() { require.NoError(t, application.closeLifecycle(context.Background())) })
	require.ErrorIs(t, builder.openSQLStore(), sqlstore.ErrImportRestricted)
}
