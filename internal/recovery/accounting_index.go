package recovery

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"math"
	"os"
	"path/filepath"

	"github.com/agentstation/starport/internal/limits/reservation"
)

// backupAccountingIndex keeps temporary aggregate state beside the private snapshot copy.
// Only the budget owner derives contributions and compares accounting values.
type backupAccountingIndex struct {
	db                *sql.DB
	tx                *sql.Tx
	corrections       int64
	linkedCorrections int64
}

func openBackupAccountingIndex(ctx context.Context, view *KVSnapshotView) (_ *backupAccountingIndex, resultErr error) {
	const name = "accounting.db"
	file, err := view.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	db, err := openKVSnapshot(filepath.Join(view.root.Name(), name), false)
	if err != nil {
		return nil, err
	}
	index := &backupAccountingIndex{db: db}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, index.Close())
		}
	}()
	// This rebuildable scratch database needs no durable transaction journal.
	if _, err := db.ExecContext(ctx, `PRAGMA journal_mode=OFF; PRAGMA synchronous=OFF; PRAGMA cache_size=-2048;
 CREATE TABLE windows (key TEXT PRIMARY KEY, record BLOB NOT NULL) STRICT, WITHOUT ROWID;
 CREATE TABLE totals (key TEXT PRIMARY KEY, consumed INTEGER NOT NULL, reserved INTEGER NOT NULL, disputes INTEGER NOT NULL, overflow INTEGER NOT NULL) STRICT, WITHOUT ROWID;`); err != nil {
		return nil, err
	}
	index.tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return index, nil
}

func (i *backupAccountingIndex) Close() error {
	if i.tx != nil {
		_ = i.tx.Rollback()
	}
	return i.db.Close()
}

func (i *backupAccountingIndex) Add(ctx context.Context, key string, record reservation.BackupRecord) error {
	if record.Kind == "correction" {
		if i.corrections == math.MaxInt64 {
			return reservation.ErrUnavailable
		}
		i.corrections++
	}
	if record.Corrections < 0 || record.Corrections > math.MaxInt64-i.linkedCorrections {
		return reservation.ErrUnavailable
	}
	i.linkedCorrections += record.Corrections
	if record.Window != nil {
		data, err := json.Marshal(record.Window)
		if err != nil {
			return err
		}
		if _, err := i.tx.ExecContext(ctx, `INSERT INTO windows(key,record) VALUES(?,?)`, key, data); err != nil {
			return err
		}
	}
	for _, contribution := range record.Contributions {
		var total reservation.BackupTotals
		err := i.tx.QueryRowContext(ctx, `SELECT consumed,reserved,disputes,overflow FROM totals WHERE key=?`, contribution.WindowKey).Scan(&total.Consumed, &total.Reserved, &total.Disputes, &total.Overflow)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		total, err = reservation.AddBackupTotals(total, contribution.Totals)
		if err != nil {
			return err
		}
		_, err = i.tx.ExecContext(ctx, `INSERT INTO totals VALUES(?,?,?,?,?) ON CONFLICT(key) DO UPDATE SET consumed=excluded.consumed,reserved=excluded.reserved,disputes=excluded.disputes,overflow=excluded.overflow`, contribution.WindowKey, total.Consumed, total.Reserved, total.Disputes, total.Overflow)
		if err != nil {
			return err
		}
	}
	return nil
}

func (i *backupAccountingIndex) Verify(ctx context.Context) (count int64, resultErr error) {
	if i.corrections != i.linkedCorrections {
		return 0, reservation.ErrUnavailable
	}
	var missing int
	if err := i.tx.QueryRowContext(ctx, `SELECT count(*) FROM totals t LEFT JOIN windows w ON w.key=t.key WHERE w.key IS NULL`).Scan(&missing); err != nil {
		return 0, err
	}
	if missing != 0 {
		return 0, reservation.ErrUnavailable
	}
	rows, err := i.tx.QueryContext(ctx, `SELECT w.record,coalesce(t.consumed,0),coalesce(t.reserved,0),coalesce(t.disputes,0),coalesce(t.overflow,0) FROM windows w LEFT JOIN totals t ON t.key=w.key ORDER BY w.key`)
	if err != nil {
		return 0, err
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	for rows.Next() {
		var data []byte
		var total reservation.BackupTotals
		if err := rows.Scan(&data, &total.Consumed, &total.Reserved, &total.Disputes, &total.Overflow); err != nil {
			return count, err
		}
		var window reservation.WindowState
		if err := json.Unmarshal(data, &window); err != nil {
			return count, err
		}
		if err := reservation.VerifyBackupTotals(window, total); err != nil {
			return count, err
		}
		count++
	}
	return count, rows.Err()
}
