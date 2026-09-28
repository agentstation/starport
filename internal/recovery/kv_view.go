package recovery

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/storage"
)

// KVSnapshotView reads a verified private image without applying current TTL expiry.
// Validation uses the recorded capture time. The view exposes no mutation methods.
type KVSnapshotView struct {
	db       *sql.DB
	parent   *os.Root
	root     *os.Root
	name     string
	identity fs.FileInfo
}

// OpenKVSnapshot verifies a private copy before exposing any records.
func OpenKVSnapshot(ctx context.Context, source, scratch string, expected KVSnapshot) (_ *KVSnapshotView, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := expected.validate(); err != nil {
		return nil, err
	}
	parent, err := productfiles.ExistingDirectory(scratch)
	if err != nil {
		return nil, err
	}
	parentRoot, err := parent.Open()
	if err != nil {
		return nil, err
	}
	name := ".kv-verify-" + rand.Text()
	_, root, err := newKVSnapshotDirectory(filepath.Join(scratch, name))
	if err != nil {
		return nil, errors.Join(err, parentRoot.Close())
	}
	info, err := parentRoot.Lstat(name)
	if err != nil {
		return nil, errors.Join(err, root.Close(), parentRoot.Close())
	}
	view := &KVSnapshotView{parent: parentRoot, root: root, name: name, identity: info}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, view.Close())
		}
	}()
	if err := copyKVSnapshot(ctx, root, source, expected); err != nil {
		return nil, err
	}
	path := KVSnapshotPath(filepath.Join(scratch, name))
	if err := validateKVSnapshot(ctx, path, expected.Records); err != nil {
		return nil, err
	}
	if err := visitKVSnapshot(ctx, path, func(storage.TransferRecord) error { return nil }); err != nil {
		return nil, err
	}
	view.db, err = openKVSnapshot(path, true)
	if err != nil {
		return nil, err
	}
	// Enumeration and its reference reads need separate immutable connections.
	view.db.SetMaxOpenConns(2)
	return view, nil
}

// GetBounded reads one retained value without filtering its recorded expiration.
func (v *KVSnapshotView) GetBounded(ctx context.Context, key string, limit int) ([]byte, error) {
	record, err := v.ReadCaptured(ctx, key, limit)
	return record.Value, err
}

// ReadCaptured returns bounded bytes and their original expiration without applying current time.
func (v *KVSnapshotView) ReadCaptured(ctx context.Context, key string, limit int) (storage.TransferRecord, error) {
	if limit <= 0 {
		return storage.TransferRecord{}, storage.ErrInvalidReadLimit
	}
	record := storage.TransferRecord{Key: key}
	var size int64
	err := v.db.QueryRowContext(ctx, "SELECT CASE WHEN length(value)<=? THEN value ELSE NULL END,length(value),expires FROM records WHERE key=?", limit, []byte(key)).Scan(&record.Value, &size, &record.ExpiresAtMillis)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.TransferRecord{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.TransferRecord{}, err
	}
	if size > int64(limit) {
		return storage.TransferRecord{}, storage.ErrValueTooLarge
	}
	return record, nil
}

// Enumerate visits each captured record in canonical key order.
func (v *KVSnapshotView) Enumerate(ctx context.Context, visit func(storage.TransferRecord) error) error {
	if visit == nil {
		return storage.ErrInvalidMutation
	}
	return visitKVRows(ctx, v.db, visit)
}

// Close releases the read connection and removes only the owned temporary image.
func (v *KVSnapshotView) Close() error {
	var err error
	if v.db != nil {
		err = v.db.Close()
	}
	err = errors.Join(err, v.root.Close())
	current, statErr := v.parent.Lstat(v.name)
	if statErr == nil {
		if !os.SameFile(v.identity, current) {
			statErr = ErrConflict
		} else {
			statErr = errors.Join(v.parent.RemoveAll(v.name), productfiles.SyncDirectory(v.parent))
		}
	}
	return errors.Join(err, statErr, v.parent.Close())
}
