package recovery

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"math"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/limits/storedbytes"
	"github.com/agentstation/starport/internal/storage"
)

// backupStoredByteIndex joins retained file claims and totals in private temporary SQL.
type backupStoredByteIndex struct{ tx *sql.Tx }

func newBackupStoredByteIndex(ctx context.Context, tx *sql.Tx) (*backupStoredByteIndex, error) {
	_, err := tx.ExecContext(ctx, `CREATE TABLE byte_accounts(holder TEXT PRIMARY KEY,total INTEGER) STRICT, WITHOUT ROWID;
 CREATE TABLE byte_claims(holder TEXT NOT NULL,id TEXT NOT NULL,held INTEGER NOT NULL,record BLOB NOT NULL,PRIMARY KEY(holder,id)) STRICT, WITHOUT ROWID;
 CREATE TABLE byte_files(holder TEXT NOT NULL,id TEXT NOT NULL,record BLOB NOT NULL,PRIMARY KEY(holder,id)) STRICT, WITHOUT ROWID;`)
	if err != nil {
		return nil, err
	}
	return &backupStoredByteIndex{tx: tx}, nil
}
func (i *backupStoredByteIndex) Add(ctx context.Context, raw storage.TransferRecord) error {
	item, err := storedbytes.VerifyRecoveryRecord(raw)
	if err != nil {
		return err
	}
	if _, err = i.tx.ExecContext(ctx, `INSERT INTO byte_accounts(holder) VALUES(?) ON CONFLICT DO NOTHING`, item.Holder); err != nil {
		return err
	}
	if item.Total != nil {
		_, err = i.tx.ExecContext(ctx, `UPDATE byte_accounts SET total=? WHERE holder=?`, *item.Total, item.Holder)
		return err
	}
	held := item.Claim.Bytes
	if item.Claim.Released {
		held = 0
	}
	data, err := json.Marshal(item.Claim)
	if err != nil {
		return err
	}
	_, err = i.tx.ExecContext(ctx, `INSERT INTO byte_claims VALUES(?,?,?,?)`, item.Holder, item.Claim.ID, held, data)
	return err
}
func (i *backupStoredByteIndex) Attach(ctx context.Context, file storedbytes.RecoveryAttachment) error {
	data, err := json.Marshal(file)
	if err != nil {
		return err
	}
	_, err = i.tx.ExecContext(ctx, `INSERT INTO byte_files VALUES(?,?,?)`, file.Holder, file.FileID, data)
	return err
}
func (i *backupStoredByteIndex) Verify(ctx context.Context) (count int64, resultErr error) {
	rows, err := i.tx.QueryContext(ctx, `SELECT a.holder,a.total,c.held FROM byte_accounts a LEFT JOIN byte_claims c ON c.holder=a.holder ORDER BY a.holder,c.id`)
	if err != nil {
		return 0, err
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	holder := ""
	var total sql.NullInt64
	var held int64
	check := func() error {
		var expected *int64
		if total.Valid {
			expected = &total.Int64
		}
		return storedbytes.VerifyRecoveryTotal(expected, held)
	}
	for rows.Next() {
		var account string
		var nextTotal, nextHeld sql.NullInt64
		if err := rows.Scan(&account, &nextTotal, &nextHeld); err != nil {
			return 0, err
		}
		if holder != "" && account != holder {
			if err := check(); err != nil {
				return 0, err
			}
			held = 0
		}
		holder, total = account, nextTotal
		if nextHeld.Valid {
			if nextHeld.Int64 < 0 || nextHeld.Int64 > math.MaxInt64-held {
				return 0, storedbytes.ErrStorageHistoryUnknown
			}
			held += nextHeld.Int64
			count++
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if holder != "" {
		if err := check(); err != nil {
			return 0, err
		}
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	return count, i.verifyAttachments(ctx)
}
func (i *backupStoredByteIndex) verifyAttachments(ctx context.Context) (resultErr error) {
	rows, err := i.tx.QueryContext(ctx, `SELECT c.record,f.record FROM byte_claims c LEFT JOIN byte_files f ON f.holder=c.holder AND f.id=c.id
 UNION ALL SELECT c.record,f.record FROM byte_files f LEFT JOIN byte_claims c ON c.holder=f.holder AND c.id=f.id`)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	for rows.Next() {
		var claimData, fileData []byte
		if err := rows.Scan(&claimData, &fileData); err != nil {
			return err
		}
		if err := verifyStoredBytePair(claimData, fileData); err != nil {
			return err
		}
	}
	return rows.Err()
}

func verifyStoredBytePair(claimData, fileData []byte) error {
	var claim *storedbytes.RecoveryClaim
	var file *storedbytes.RecoveryAttachment
	if claimData != nil {
		if err := json.Unmarshal(claimData, &claim); err != nil {
			return err
		}
	}
	if fileData != nil {
		if err := json.Unmarshal(fileData, &file); err != nil {
			return err
		}
	}
	return storedbytes.VerifyRecoveryAttachment(claim, file)
}
func inspectStoredFile(ctx context.Context, record storage.TransferRecord, blobs blob.SnapshotView, capturedAt time.Time, index *backupStoredByteIndex) error {
	if record.ExpiresAtMillis != 0 {
		return files.ErrCorruptRecord
	}
	file, err := files.VerifyRecoveryRecord(ctx, record.Key, record.Value, blobs, capturedAt)
	if err != nil {
		return err
	}
	reader, ok := blobs.(blob.RecoveryPublicationReader)
	if !ok {
		return files.ErrCorruptRecord
	}
	attachment, err := file.VerifyRecoveryByteAttachment(ctx, reader)
	if err != nil {
		return err
	}
	return index.Attach(ctx, attachment)
}
