package sqlstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
)

// RelationalSnapshotView owns a verified private copy of a portable SQL image.
// Its connection is immutable and read-only. It exposes no transaction or write methods.
type RelationalSnapshotView struct {
	db       *sql.DB
	parent   *os.Root
	name     string
	identity fs.FileInfo
}

// OpenRelationalSnapshot verifies bytes and the complete schema before exposing records.
// Scratch must be an existing private directory. Close removes only the owned copy.
func OpenRelationalSnapshot(ctx context.Context, source string, expected SQLiteSnapshot, scratch string) (_ *RelationalSnapshotView, resultErr error) {
	parent, err := productfiles.ExistingDirectory(scratch)
	if err != nil {
		return nil, err
	}
	root, err := parent.Open()
	if err != nil {
		return nil, err
	}
	name := ".relational-view-" + rand.Text()
	destination := filepath.Join(scratch, name)
	result, copyErr := RestoreSQLiteSnapshot(ctx, destination, source, expected)
	if !result.Published {
		return nil, errors.Join(copyErr, root.Close())
	}
	identity, err := root.Lstat(name)
	if err != nil {
		return nil, errors.Join(copyErr, err, root.Close())
	}
	view := &RelationalSnapshotView{parent: root, name: name, identity: identity}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, view.Close())
		}
	}()
	if copyErr != nil {
		return nil, copyErr
	}
	view.db, err = sql.Open("sqlite", "file:"+url.PathEscape(filepath.Join(destination, "starport.db"))+"?mode=ro&immutable=1&_pragma=trusted_schema(0)")
	if err != nil {
		return nil, err
	}
	// An owner can query a reference while enumerating one immutable table.
	view.db.SetMaxOpenConns(2)
	if err := validateRelationalSchema(ctx, view.db, TypeSQLite); err != nil {
		return nil, err
	}
	return view, nil
}

// QueryContext reads the immutable copy through SQLite's read-only connection.
func (v *RelationalSnapshotView) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return v.db.QueryContext(ctx, query, args...)
}

// QueryRowContext reads one row from the immutable copy.
func (v *RelationalSnapshotView) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return v.db.QueryRowContext(ctx, query, args...)
}

// Close releases connections and removes only the directory with the captured identity.
func (v *RelationalSnapshotView) Close() error {
	var result error
	if v.db != nil {
		result = v.db.Close()
	}
	current, err := v.parent.Lstat(v.name)
	if err == nil {
		if !os.SameFile(v.identity, current) {
			err = errors.New("relational verification directory identity changed")
		} else {
			err = errors.Join(v.parent.RemoveAll(v.name), productfiles.SyncDirectory(v.parent))
		}
	}
	return errors.Join(result, err, v.parent.Close())
}
