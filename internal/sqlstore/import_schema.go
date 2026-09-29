package sqlstore

import (
	"context"
	"errors"
)

// PrepareImportSchema initializes empty target tables or accepts a current schema.
// It never upgrades populated state. All writers and other schema owners must remain fenced.
// The importer separately checks emptiness or the exact retained import receipt.
func (db *DB) PrepareImportSchema(ctx context.Context) error {
	if db == nil || db.DB == nil {
		return ErrClosed
	}
	if err := validateRelationalSchema(ctx, db, db.dialect); err == nil {
		return nil
	}
	actual, err := relationalObjects(ctx, db, db.dialect)
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(relationalTables)+1)
	for _, table := range relationalTables {
		known[table.name] = true
	}
	if db.dialect == TypeMySQL {
		known["schema_migration_attempts"] = true
	}
	for name := range actual {
		if !known[name] {
			return errors.New("unknown relational object prevents import initialization")
		}
		if name == migrationTable {
			continue
		}
		query := "SELECT COUNT(*) FROM " + name // #nosec G202 -- the compiled table contract supplies every allowed identifier.
		if name == metadataTable {
			query += " WHERE name IS NULL OR value IS NULL OR name <> 'schema' OR value <> 'starport'"
		}
		var count int64
		if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			if name == "schema_migration_attempts" {
				return ErrMigrationRecoveryRequired
			}
			return ErrNotFresh
		}
	}
	return db.Migrate(ctx)
}
