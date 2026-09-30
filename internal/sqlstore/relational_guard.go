package sqlstore

import (
	"context"
	"database/sql"
	"errors"
)

// ErrGuardDeadline refuses an import guard without a context deadline.
var ErrGuardDeadline = errors.New("relational import guard requires a context deadline")

// GuardRelationalImport holds native migration ownership and a transaction at an exact closed import position.
// The callback must use only conn for SQL work, without a nested transaction, commit, or schema change.
// The caller must bound non-SQL work by the context deadline and retain receipts for independent commits.
// A returned error does not undo an independent KV commit or prove that SQL completion failed.
// This guard never advances replay or removes the import barrier.
func (db *DB) GuardRelationalImport(ctx context.Context, snapshot SQLiteSnapshot, identity RelationalImportIdentity, position RelationalReplayPosition, callback func(context.Context, *sql.Conn) error) (resultErr error) {
	if db == nil || db.DB == nil || ctx == nil {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return ErrGuardDeadline
	}
	if callback == nil {
		return ErrImportRestricted
	}
	claim, err := inspectionClaim(ctx, snapshot, identity, position)
	if err != nil {
		return err
	}
	owner, err := db.acquireMigrationOwner(ctx)
	if err != nil {
		return errors.Join(err, ctx.Err())
	}
	defer func() { resultErr = errors.Join(resultErr, owner.close(), ctx.Err()) }()
	return owner.transaction(ctx, func() error {
		if err := db.checkImportPosition(ctx, owner.conn, claim, position); err != nil {
			return err
		}
		if err := callback(ctx, owner.conn); err != nil {
			return err
		}
		return db.checkImportPosition(ctx, owner.conn, claim, position)
	})
}
