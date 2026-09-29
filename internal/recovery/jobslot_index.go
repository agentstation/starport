package recovery

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"

	"github.com/agentstation/starport/internal/limits/jobslots"
	"github.com/agentstation/starport/internal/storage"
)

// backupJobSlotIndex joins retained claims and work without keeping a fleet in memory.
// The slot owner validates all schemas, totals, and attachment transitions.
type backupJobSlotIndex struct {
	tx     *sql.Tx
	report jobslots.RecoveryReport
}

func newBackupJobSlotIndex(ctx context.Context, tx *sql.Tx) (*backupJobSlotIndex, error) {
	// The accounting index owns this private, temporary database and transaction.
	_, err := tx.ExecContext(ctx, `
 CREATE TABLE slot_accounts (account TEXT PRIMARY KEY, total INTEGER, history INTEGER NOT NULL DEFAULT 0, held INTEGER NOT NULL DEFAULT 0) STRICT, WITHOUT ROWID;
 CREATE TABLE slot_claims (account TEXT NOT NULL, id TEXT NOT NULL, kind TEXT NOT NULL, job TEXT NOT NULL, record BLOB NOT NULL, PRIMARY KEY(account,id)) STRICT, WITHOUT ROWID;
 CREATE TABLE slot_attachments (account TEXT NOT NULL, id TEXT NOT NULL, kind TEXT NOT NULL, job TEXT NOT NULL, record BLOB NOT NULL, PRIMARY KEY(account,kind,job)) STRICT, WITHOUT ROWID;
 CREATE UNIQUE INDEX slot_work_claim ON slot_attachments(account,id) WHERE id<>'';`)
	if err != nil {
		return nil, err
	}
	return &backupJobSlotIndex{tx: tx}, nil
}

func (i *backupJobSlotIndex) Add(ctx context.Context, record storage.TransferRecord) error {
	item, err := jobslots.VerifyRecoveryRecord(record)
	if err != nil {
		return err
	}
	if _, err := i.tx.ExecContext(ctx, `INSERT INTO slot_accounts(account) VALUES(?) ON CONFLICT DO NOTHING`, item.Account); err != nil {
		return err
	}
	switch {
	case item.Total != nil:
		_, err = i.tx.ExecContext(ctx, `UPDATE slot_accounts SET total=? WHERE account=?`, *item.Total, item.Account)
	case item.History:
		_, err = i.tx.ExecContext(ctx, `UPDATE slot_accounts SET history=1 WHERE account=?`, item.Account)
	case item.Claim != nil:
		data, encodeErr := json.Marshal(item.Claim)
		if encodeErr != nil {
			return encodeErr
		}
		_, err = i.tx.ExecContext(ctx, `INSERT INTO slot_claims VALUES(?,?,?,?,?)`, item.Account, item.Claim.ID, item.Claim.Kind, item.Claim.JobID, data)
		if err != nil {
			return err
		}
		i.report.Claims++
		if !item.Claim.Released {
			_, err = i.tx.ExecContext(ctx, `UPDATE slot_accounts SET held=held+1 WHERE account=?`, item.Account)
			i.report.Held++
			if !item.Claim.Attached {
				i.report.Pending++
			}
		}
	}
	return err
}

func (i *backupJobSlotIndex) Attach(ctx context.Context, work jobslots.RecoveryAttachment) error {
	if work.ClaimID == "" && work.Released {
		return jobslots.ErrHistoryUnknown
	}
	data, err := json.Marshal(work)
	if err != nil {
		return err
	}
	_, err = i.tx.ExecContext(ctx, `INSERT INTO slot_attachments VALUES(?,?,?,?,?)`, work.Account, work.ClaimID, work.Kind, work.JobID, data)
	if err == nil && work.ClaimID != "" {
		i.report.Attachments++
	}
	return err
}

func (i *backupJobSlotIndex) Verify(ctx context.Context) (jobslots.RecoveryReport, error) {
	if err := i.verifyTotals(ctx); err != nil {
		return i.report, err
	}
	return i.report, i.verifyAttachments(ctx)
}

func (i *backupJobSlotIndex) verifyTotals(ctx context.Context) (resultErr error) {
	rows, err := i.tx.QueryContext(ctx, `SELECT total,history,held FROM slot_accounts ORDER BY account`)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	for rows.Next() {
		var total sql.NullInt64
		var history bool
		var held int64
		if err := rows.Scan(&total, &history, &held); err != nil {
			return err
		}
		var count *int64
		if total.Valid {
			count = &total.Int64
		}
		if err := jobslots.VerifyRecoveryTotal(count, history, held); err != nil {
			return err
		}
		i.report.Accounts++
	}
	return rows.Err()
}

func (i *backupJobSlotIndex) verifyAttachments(ctx context.Context) (resultErr error) {
	rows, err := i.tx.QueryContext(ctx, `SELECT c.record,a.record FROM slot_claims c
 LEFT JOIN slot_attachments a ON a.account=c.account AND a.kind=c.kind AND a.job=c.job
 UNION ALL
 SELECT NULL,a.record FROM slot_attachments a
 LEFT JOIN slot_claims c ON c.account=a.account AND c.id=a.id
 WHERE a.id<>'' AND c.id IS NULL`)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	for rows.Next() {
		var claimData, workData []byte
		if err := rows.Scan(&claimData, &workData); err != nil {
			return err
		}
		var claim *jobslots.Claim
		var work *jobslots.RecoveryAttachment
		if claimData != nil {
			if err := json.Unmarshal(claimData, &claim); err != nil {
				return err
			}
		}
		if workData != nil {
			if err := json.Unmarshal(workData, &work); err != nil {
				return err
			}
		}
		if err := jobslots.VerifyRecoveryAttachment(claim, work); err != nil {
			return err
		}
	}
	return rows.Err()
}
