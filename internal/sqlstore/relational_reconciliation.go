package sqlstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"strings"
	"unicode"
)

// ReconcileRelationalImport commits one domain repair and its immutable receipt.
// It requires the exact import barrier and retains that barrier after success.
// apply must use conn without committing, changing schema, or opening admission.
// The owner must validate current domain state after an exact receipt retry.
func (db *DB) ReconcileRelationalImport(ctx context.Context, snapshot SQLiteSnapshot, identity RelationalImportIdentity, step, evidenceSHA256 string, apply func(context.Context, *sql.Conn) error) (resultErr error) {
	if db == nil || db.DB == nil || ctx == nil {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if apply == nil || strings.TrimSpace(step) == "" || len(step) > 256 || strings.ContainsFunc(step, unicode.IsControl) ||
		!relationalActivationDigest(evidenceSHA256) || !relationalActivationDigest(snapshot.SHA256) || snapshot.Format != sqliteSnapshotFormat || snapshot.Size <= 0 {
		return ErrImportRestricted
	}
	claim, err := relationalImportClaim(snapshot, identity)
	if err != nil {
		return err
	}
	claimDigest, stepDigest := sha256.Sum256(claim), sha256.Sum256([]byte(step))
	claimSHA256 := hex.EncodeToString(claimDigest[:])
	key := "relational-reconciliation-v1:" + claimSHA256 + ":" + hex.EncodeToString(stepDigest[:])
	receipt, err := json.Marshal(struct {
		Version        int    `json:"version"`
		ClaimSHA256    string `json:"claim_sha256"`
		EvidenceSHA256 string `json:"evidence_sha256"`
	}{1, claimSHA256, evidenceSHA256})
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
		if err != nil || marker != string(claim) {
			return errors.Join(ErrImportRestricted, err)
		}
		current, err := db.readActivationReceipt(ctx, owner.conn, relationalActivationCurrent)
		if err != nil || current != "" {
			return errors.Join(ErrImportRestricted, err)
		}
		prior, err := db.readActivationReceipt(ctx, owner.conn, key)
		if err != nil {
			return err
		}
		if prior != "" {
			if prior != string(receipt) {
				return ErrImportRestricted
			}
			return nil
		}
		if err := apply(ctx, owner.conn); err != nil {
			return err
		}
		marker, err = readRelationalImport(ctx, owner.conn)
		if err != nil || marker != string(claim) {
			return errors.Join(ErrImportRestricted, err)
		}
		current, err = db.readActivationReceipt(ctx, owner.conn, relationalActivationCurrent)
		if err != nil || current != "" {
			return errors.Join(ErrImportRestricted, err)
		}
		_, err = owner.conn.ExecContext(ctx, db.Bind("INSERT INTO sqlstore_meta(name,value) VALUES(?,?)"), key, string(receipt))
		return err
	})
}
