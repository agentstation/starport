package sqlstore

import (
	"context"
	"database/sql"
	"errors"
)

// RelationalReplayPosition selects an exact retained replay cursor.
// The zero value selects imported state before the first replay step.
type RelationalReplayPosition struct {
	Sequence      int64  `json:"sequence"`
	ReceiptSHA256 string `json:"receipt_sha256"`
}

func (p RelationalReplayPosition) valid() bool {
	return p.Sequence == 0 && p.ReceiptSHA256 == "" || p.Sequence > 0 && relationalActivationDigest(p.ReceiptSHA256)
}

// SnapshotRelationalImport captures a closed import at its exact replay position.
// Writers must remain fenced. The result preserves native import controls for inspection.
// Failure returns no valid snapshot receipt and never releases the import barrier.
func (db *DB) SnapshotRelationalImport(ctx context.Context, destination string, original SQLiteSnapshot, identity RelationalImportIdentity, position RelationalReplayPosition) (SQLiteSnapshotResult, error) {
	claim, err := inspectionClaim(ctx, original, identity, position)
	if err != nil {
		return SQLiteSnapshotResult{}, err
	}
	result, err := db.snapshotRelational(ctx, destination, func(ctx context.Context, conn *sql.Conn) error {
		return db.checkImportPosition(ctx, conn, claim, position)
	})
	if err != nil {
		result.Snapshot = SQLiteSnapshot{}
	}
	return result, err
}

// CheckRelationalImportPosition verifies current closed ownership without changing state.
// It does not prove external fencing or complete independent history.
func (db *DB) CheckRelationalImportPosition(ctx context.Context, original SQLiteSnapshot, identity RelationalImportIdentity, position RelationalReplayPosition) (resultErr error) {
	if db == nil || db.DB == nil {
		return ErrClosed
	}
	claim, err := inspectionClaim(ctx, original, identity, position)
	if err != nil {
		return err
	}
	owner, err := db.acquireMigrationOwner(ctx)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, owner.close()) }()
	return db.checkImportPosition(ctx, owner.conn, claim, position)
}

func inspectionClaim(ctx context.Context, original SQLiteSnapshot, identity RelationalImportIdentity, position RelationalReplayPosition) ([]byte, error) {
	if ctx == nil {
		return nil, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !position.valid() || original.Format != sqliteSnapshotFormat || original.Size <= 0 || !relationalActivationDigest(original.SHA256) {
		return nil, ErrImportRestricted
	}
	return relationalImportClaim(original, identity)
}

func (db *DB) checkImportPosition(ctx context.Context, conn *sql.Conn, claim []byte, position RelationalReplayPosition) error {
	if err := db.verifyReplayBarrier(ctx, conn, claim); err != nil {
		return err
	}
	cursor, data, err := db.readReplayCursor(ctx, conn, relationalReplayDigest(claim))
	if err != nil {
		return err
	}
	if cursor.Sequence != position.Sequence || position.Sequence > 0 && relationalReplayDigest([]byte(data)) != position.ReceiptSHA256 {
		return ErrImportRestricted
	}
	return ctx.Err()
}
