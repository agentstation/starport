package sqlstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
)

const (
	relationalActivationPrefix  = "relational-activation-v1:"
	relationalActivationCurrent = "relational-activation-current-v1"
)

type relationalActivationReceipt struct {
	Version        int                       `json:"version"`
	ClaimSHA256    string                    `json:"claim_sha256"`
	DecisionSHA256 string                    `json:"decision_sha256"`
	Position       *RelationalReplayPosition `json:"position,omitempty"`
}

// ActivateRelationalImport commits final domain approval, its receipt, and import
// barrier removal together. The coordinator must first verify history and fencing.
// approve must use only conn for SQL writes and must not commit or change schema.
// Exact retries verify completion without repeating approval or changing later data.
func (db *DB) ActivateRelationalImport(ctx context.Context, snapshot SQLiteSnapshot, identity RelationalImportIdentity, decisionSHA256 string, approve func(context.Context, *sql.Conn) error) (resultErr error) {
	return db.activateRelationalImport(ctx, snapshot, identity, nil, decisionSHA256, approve, false)
}

// ActivateRelationalImportAt binds barrier release to the exact final replay position.
// The transaction checks the retained cursor before and after the approval callback.
// Exact retries preserve later SQL changes without repeating the callback.
func (db *DB) ActivateRelationalImportAt(ctx context.Context, snapshot SQLiteSnapshot, identity RelationalImportIdentity, position RelationalReplayPosition, decisionSHA256 string, approve func(context.Context, *sql.Conn) error) error {
	if !position.valid() {
		return ErrImportRestricted
	}
	return db.activateRelationalImport(ctx, snapshot, identity, &position, decisionSHA256, approve, false)
}

// CheckActivatedRelationalImportAt verifies the exact retained native activation.
// It does not approve admission. It preserves all later domain changes and withdrawals.
func (db *DB) CheckActivatedRelationalImportAt(ctx context.Context, snapshot SQLiteSnapshot, identity RelationalImportIdentity, position RelationalReplayPosition, decisionSHA256 string) error {
	if !position.valid() {
		return ErrImportRestricted
	}
	return db.activateRelationalImport(ctx, snapshot, identity, &position, decisionSHA256, nil, true)
}

func (db *DB) activateRelationalImport(ctx context.Context, snapshot SQLiteSnapshot, identity RelationalImportIdentity, position *RelationalReplayPosition, decisionSHA256 string, approve func(context.Context, *sql.Conn) error, inspect bool) (resultErr error) {
	if db == nil || db.DB == nil || ctx == nil {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !inspect && approve == nil {
		return ErrImportRestricted
	}
	claim, receipt, encoded, err := newRelationalActivationReceipt(snapshot, identity, position, decisionSHA256)
	if err != nil {
		return err
	}
	owner, err := db.acquireMigrationOwner(ctx)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, owner.close()) }()
	return owner.transaction(ctx, func() error {
		if err := db.checkActivationReplayPosition(ctx, owner.conn, claim, position); err != nil {
			return err
		}
		marker, err := readRelationalImport(ctx, owner.conn)
		if err != nil {
			return err
		}
		current, err := db.readActivationReceipt(ctx, owner.conn, relationalActivationCurrent)
		if err != nil {
			return err
		}
		key := relationalActivationPrefix + receipt.ClaimSHA256
		history, err := db.readActivationReceipt(ctx, owner.conn, key)
		if err != nil {
			return err
		}
		if marker == "" {
			if current != string(encoded) || history != string(encoded) {
				return ErrImportRestricted
			}
			return nil
		}
		if inspect || marker != string(claim) || current != "" || history != "" {
			return ErrImportRestricted
		}
		if err := approve(ctx, owner.conn); err != nil {
			return err
		}
		if err := db.checkActivationReplayPosition(ctx, owner.conn, claim, position); err != nil {
			return err
		}
		for _, name := range []string{key, relationalActivationCurrent} {
			if _, err := owner.conn.ExecContext(ctx, db.Bind("INSERT INTO sqlstore_meta(name,value) VALUES(?,?)"), name, string(encoded)); err != nil {
				return err
			}
		}
		deleted, err := owner.conn.ExecContext(ctx, db.Bind("DELETE FROM sqlstore_meta WHERE name=? AND value=?"), relationalImportMarker, string(claim))
		if err != nil {
			return err
		}
		count, err := deleted.RowsAffected()
		if err != nil || count != 1 {
			return errors.Join(ErrImportRestricted, err)
		}
		return nil
	})
}

func (db *DB) checkActivationReplayPosition(ctx context.Context, conn *sql.Conn, claim []byte, position *RelationalReplayPosition) error {
	if position == nil {
		return nil
	}
	cursor, encoded, err := db.readReplayCursor(ctx, conn, relationalReplayDigest(claim))
	if err != nil {
		return err
	}
	if cursor.Sequence != position.Sequence || position.Sequence > 0 && relationalReplayDigest([]byte(encoded)) != position.ReceiptSHA256 {
		return ErrImportRestricted
	}
	return ctx.Err()
}

func (db *DB) readActivationReceipt(ctx context.Context, conn *sql.Conn, name string) (string, error) {
	var value string
	err := conn.QueryRowContext(ctx, db.Bind("SELECT SUBSTR(value,1,513) FROM sqlstore_meta WHERE name=?"), name).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if len(value) == 0 || len(value) > 512 {
		return "", ErrImportRestricted
	}
	return value, nil
}

func relationalActivationDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func newRelationalActivationReceipt(snapshot SQLiteSnapshot, identity RelationalImportIdentity, position *RelationalReplayPosition, decisionSHA256 string) ([]byte, relationalActivationReceipt, []byte, error) {
	if !relationalActivationDigest(decisionSHA256) || !relationalActivationDigest(snapshot.SHA256) || snapshot.Format != sqliteSnapshotFormat || snapshot.Size <= 0 {
		return nil, relationalActivationReceipt{}, nil, ErrImportRestricted
	}
	claim, err := relationalImportClaim(snapshot, identity)
	if err != nil {
		return nil, relationalActivationReceipt{}, nil, err
	}
	digest := sha256.Sum256(claim)
	receipt := relationalActivationReceipt{Version: 1, ClaimSHA256: hex.EncodeToString(digest[:]), DecisionSHA256: decisionSHA256}
	if position != nil {
		receipt.Version = 2
		copied := *position
		receipt.Position = &copied
	}
	encoded, err := json.Marshal(receipt)
	return claim, receipt, encoded, err
}
