package recovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/storage"
)

const (
	kvSnapshotFormat = "starport-kv-snapshot-v1"
	kvSnapshotFile   = "kv.db"
	kvSnapshotSchema = "CREATE TABLE records (key BLOB PRIMARY KEY, value BLOB NOT NULL, expires INTEGER NOT NULL) STRICT, WITHOUT ROWID"
)

// KVSnapshot identifies one portable, deduplicated KV image.
// The complete deployment manifest must bind this receipt to its other stores.
type KVSnapshot struct {
	Format  string `json:"format"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	Records int64  `json:"records"`
}

// SnapshotKV writes kv.db and its receipt into a new private directory.
// Writers must remain stopped. Partial directories are not valid backups.
// SnapshotKV publishes the receipt only after the complete image reaches durable storage.
func SnapshotKV(ctx context.Context, source storage.RecordSource, destination string) (receipt KVSnapshot, resultErr error) {
	if err := ctx.Err(); err != nil {
		return receipt, err
	}
	if source == nil {
		return receipt, errors.New("KV snapshot requires a record source")
	}
	directory, root, err := newKVSnapshotDirectory(destination)
	if err != nil {
		return receipt, err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	file, err := root.OpenFile(kvSnapshotFile, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return receipt, err
	}
	if err := file.Close(); err != nil {
		return receipt, err
	}
	path := filepath.Join(destination, kvSnapshotFile)
	count, err := collectKVSnapshot(ctx, source, path)
	if err != nil {
		return receipt, err
	}
	if err := validateKVSnapshot(ctx, path, count); err != nil {
		return receipt, err
	}
	file, err = root.OpenFile(kvSnapshotFile, os.O_RDWR, 0)
	if err != nil {
		return receipt, err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	hash := sha256.New()
	size, err := io.Copy(hash, &kvSnapshotReader{ctx: ctx, reader: file})
	if err != nil {
		return receipt, err
	}
	if err := file.Sync(); err != nil {
		return receipt, err
	}
	if err := productfiles.SyncDirectory(root); err != nil {
		return receipt, err
	}
	receipt = KVSnapshot{Format: kvSnapshotFormat, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil)), Records: count}
	body, err := json.Marshal(receipt)
	if err != nil {
		return KVSnapshot{}, err
	}
	if err := directory.CompareAndPublish(ctx, "snapshot.json", nil, body); err != nil {
		return KVSnapshot{}, err
	}
	return receipt, nil
}

func newKVSnapshotDirectory(destination string) (*productfiles.Directory, *os.Root, error) {
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return nil, nil, errors.New("KV snapshot requires a clean absolute destination")
	}
	parent, err := productfiles.ExistingDirectory(filepath.Dir(destination))
	if err != nil {
		return nil, nil, err
	}
	directory, err := parent.CreateChild(filepath.Base(destination))
	if err != nil {
		return nil, nil, err
	}
	root, err := directory.Open()
	return directory, root, err
}

func openKVSnapshot(path string, readonly bool) (*sql.DB, error) {
	options := "?mode=rw&_pragma=trusted_schema(0)&_pragma=journal_mode(DELETE)&_pragma=synchronous(FULL)"
	if readonly {
		options = "?mode=ro&immutable=1&_pragma=trusted_schema(0)"
	}
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(path)+options)
	if err == nil {
		db.SetMaxOpenConns(1)
	}
	return db, err
}

func collectKVSnapshot(ctx context.Context, source storage.RecordSource, path string) (count int64, resultErr error) {
	db, err := openKVSnapshot(path, false)
	if err != nil {
		return 0, err
	}
	defer func() { resultErr = errors.Join(resultErr, db.Close()) }()
	if _, err := db.ExecContext(ctx, kvSnapshotSchema); err != nil {
		return 0, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	err = source.Enumerate(ctx, func(record storage.TransferRecord) error {
		if err := record.Validate(); err != nil {
			return err
		}
		value := record.Value
		if value == nil {
			value = []byte{}
		}
		result, err := tx.ExecContext(ctx, "INSERT INTO records(key,value,expires) VALUES(?,?,?) ON CONFLICT(key) DO NOTHING", []byte(record.Key), value, record.ExpiresAtMillis)
		if err != nil {
			return err
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if inserted == 1 {
			count++
			return nil
		}
		var previous []byte
		var expires int64
		if err := tx.QueryRowContext(ctx, "SELECT value,expires FROM records WHERE key=?", []byte(record.Key)).Scan(&previous, &expires); err != nil {
			return err
		}
		if !bytes.Equal(previous, value) || expires != record.ExpiresAtMillis {
			return errors.New("KV source changed during snapshot")
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, tx.Commit()
}

func validateKVSnapshot(ctx context.Context, path string, expectedCount int64) (resultErr error) {
	db, err := openKVSnapshot(path, true)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, db.Close()) }()
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return errors.New("KV snapshot integrity check failed")
	}
	rows, err := db.QueryContext(ctx, "SELECT type,name,sql FROM sqlite_schema")
	if err != nil {
		return err
	}
	var entries int
	for rows.Next() {
		var kind, name, definition string
		if err := rows.Scan(&kind, &name, &definition); err != nil {
			return errors.Join(err, rows.Close())
		}
		if kind != "table" || name != "records" || definition != kvSnapshotSchema {
			return errors.Join(errors.New("unsupported KV snapshot schema"), rows.Close())
		}
		entries++
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if entries != 1 {
		return errors.New("KV snapshot record table is missing")
	}
	var count, invalid int64
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM records").Scan(&count); err != nil {
		return err
	}
	if count != expectedCount {
		return errors.New("KV snapshot record count mismatch")
	}
	// Check lengths and SQLite storage classes before selecting payloads into Go.
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM records WHERE
typeof(key) != 'blob' OR typeof(value) != 'blob' OR typeof(expires) != 'integer'
OR length(key) < 1 OR length(key) > ? OR length(value) > ?
OR key = ? OR expires < 0 OR expires > 253402300799999`, storage.TransferMaxKeyBytes, storage.TransferMaxValueBytes, []byte(storage.TransferBarrierKey)).Scan(&invalid); err != nil {
		return err
	}
	if invalid != 0 {
		return errors.New("KV snapshot contains invalid transfer records")
	}
	return nil
}

func (s KVSnapshot) validate() error {
	digest, err := hex.DecodeString(s.SHA256)
	if err != nil || len(digest) != sha256.Size || s.Format != kvSnapshotFormat || s.Size <= 0 || s.Records < 0 {
		return errors.New("invalid KV snapshot identity")
	}
	return nil
}

type kvSnapshotReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *kvSnapshotReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func copyKVSnapshot(ctx context.Context, root *os.Root, source string, expected KVSnapshot) (resultErr error) {
	input, err := os.Open(source) // #nosec G304 -- the operator selects the snapshot path. This function verifies its bytes before use.
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, input.Close()) }()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != expected.Size {
		return errors.New("KV snapshot size or file type mismatch")
	}
	output, err := root.OpenFile(kvSnapshotFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, output.Close()) }()
	hash := sha256.New()
	if _, err := io.CopyN(io.MultiWriter(output, hash), &kvSnapshotReader{ctx: ctx, reader: input}, expected.Size); err != nil {
		return err
	}
	var extra [1]byte
	if n, err := input.Read(extra[:]); n != 0 || !errors.Is(err, io.EOF) {
		return errors.New("KV snapshot changed during copy")
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
		return errors.New("KV snapshot digest mismatch")
	}
	return output.Sync()
}

// KVSnapshotPath names the portable image inside a published snapshot directory.
func KVSnapshotPath(directory string) string { return filepath.Join(directory, kvSnapshotFile) }

func visitKVSnapshot(ctx context.Context, path string, visit func(storage.TransferRecord) error) (resultErr error) {
	db, err := openKVSnapshot(path, true)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, db.Close()) }()
	rows, err := db.QueryContext(ctx, "SELECT key,value,expires FROM records ORDER BY key")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	for rows.Next() {
		var record storage.TransferRecord
		var key []byte
		if err := rows.Scan(&key, &record.Value, &record.ExpiresAtMillis); err != nil {
			return err
		}
		record.Key = string(key)
		if err := record.Validate(); err != nil {
			return fmt.Errorf("invalid KV snapshot record: %w", err)
		}
		if err := visit(record); err != nil {
			return err
		}
	}
	return rows.Err()
}
