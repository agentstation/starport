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
	Version        int    `json:"version"`
	ClaimSHA256    string `json:"claim_sha256"`
	DecisionSHA256 string `json:"decision_sha256"`
}

// ActivateRelationalImport commits final domain approval, its receipt, and import
// barrier removal together. The coordinator must first verify history and fencing.
// approve must use only conn for SQL writes and must not commit or change schema.
// Exact retries verify completion without repeating approval or changing later data.
func (db *DB) ActivateRelationalImport(ctx context.Context, snapshot SQLiteSnapshot, identity RelationalImportIdentity, decisionSHA256 string, approve func(context.Context, *sql.Conn) error) (resultErr error) {
	if db == nil || db.DB == nil || ctx == nil {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if approve == nil || !relationalActivationDigest(decisionSHA256) || !relationalActivationDigest(snapshot.SHA256) || snapshot.Format != sqliteSnapshotFormat || snapshot.Size <= 0 {
		return ErrImportRestricted
	}
	claim, err := relationalImportClaim(snapshot, identity)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(claim)
	receipt := relationalActivationReceipt{Version: 1, ClaimSHA256: hex.EncodeToString(digest[:]), DecisionSHA256: decisionSHA256}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	owner, err := db.acquireMigrationOwner(ctx)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, owner.close()) }()
	return owner.transaction(ctx, func() error {
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
		if marker != string(claim) || current != "" || history != "" {
			return ErrImportRestricted
		}
		if err := approve(ctx, owner.conn); err != nil {
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

func (db *DB) readActivationReceipt(ctx context.Context, conn *sql.Conn, name string) (string, error) {
	var value string
	err := conn.QueryRowContext(ctx, db.Bind("SELECT SUBSTR(value,1,257) FROM sqlstore_meta WHERE name=?"), name).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if len(value) == 0 || len(value) > 256 {
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
