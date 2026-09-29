package sqlstore

import (
	"context"
	"crypto/rand"
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
)

const sqliteSnapshotFormat = "sqlite-snapshot-v1"

// SQLiteSnapshot identifies a complete database image, including committed WAL data.
// The deployment manifest must bind this identity to the other persistent stores.
type SQLiteSnapshot struct {
	Format string `json:"format"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// SQLiteSnapshotResult reports publication separately from the final directory sync.
// Published can be true with an error when durability remains uncertain.
type SQLiteSnapshotResult struct {
	Snapshot  SQLiteSnapshot
	Published bool
}

// SnapshotSQLite publishes a consistent SQLite image into a new private directory.
// The directory contains starport.db and snapshot.json. Existing destinations remain untouched.
// The deployment coordinator must stop cross-store writes before this operation.
func (db *DB) SnapshotSQLite(ctx context.Context, destination string) (SQLiteSnapshotResult, error) {
	if db == nil || db.DB == nil {
		return SQLiteSnapshotResult{}, ErrClosed
	}
	if db.dialect != TypeSQLite {
		return SQLiteSnapshotResult{}, errors.New("SQLite snapshot requires a SQLite database")
	}
	return publishSQLiteSnapshot(ctx, destination, func(root *os.Root, path string) (SQLiteSnapshot, error) {
		file, err := root.OpenFile("starport.db", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err != nil {
			return SQLiteSnapshot{}, err
		}
		if err := file.Close(); err != nil {
			return SQLiteSnapshot{}, err
		}
		// SQLite reads committed WAL frames while producing one consistent main image.
		if _, err := db.ExecContext(ctx, "VACUUM main INTO ?", filepath.Join(path, "starport.db")); err != nil {
			return SQLiteSnapshot{}, err
		}
		return inspectSQLiteSnapshot(ctx, root, path)
	})
}

// RestoreSQLiteSnapshot verifies an image and publishes it into a new private directory.
// This imports bytes only. It does not approve recovery or select the database for a running gateway.
// The caller must fence old writers and reconcile independent history before admission.
func RestoreSQLiteSnapshot(ctx context.Context, destination, source string, expected SQLiteSnapshot) (SQLiteSnapshotResult, error) {
	digest, err := hex.DecodeString(expected.SHA256)
	if err != nil || len(digest) != sha256.Size || expected.Size <= 0 || expected.Format != sqliteSnapshotFormat {
		return SQLiteSnapshotResult{}, errors.New("invalid SQLite snapshot identity")
	}
	return publishSQLiteSnapshot(ctx, destination, func(root *os.Root, path string) (SQLiteSnapshot, error) {
		if err := copySQLiteSnapshot(ctx, root, source, expected); err != nil {
			return SQLiteSnapshot{}, err
		}
		actual, err := inspectSQLiteSnapshot(ctx, root, path)
		if err != nil {
			return SQLiteSnapshot{}, err
		}
		if actual != expected {
			return SQLiteSnapshot{}, errors.New("SQLite snapshot identity mismatch")
		}
		return actual, nil
	})
}

func publishSQLiteSnapshot(ctx context.Context, destination string, prepare func(*os.Root, string) (SQLiteSnapshot, error)) (result SQLiteSnapshotResult, resultErr error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return result, errors.New("SQLite snapshot requires a clean absolute destination")
	}
	parent, err := productfiles.ExistingDirectory(filepath.Dir(destination))
	if err != nil {
		return result, err
	}
	root, err := parent.Open()
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	name := filepath.Base(destination)
	if _, err := root.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		return result, errors.Join(os.ErrExist, err)
	}
	stageName := ".sqlite-snapshot-" + rand.Text()
	stage, err := parent.CreateChild(stageName)
	if err != nil {
		return result, err
	}
	stageRoot, err := stage.Open()
	if err != nil {
		return result, err
	}
	identity, err := root.Lstat(stageName)
	if err != nil {
		return result, errors.Join(err, stageRoot.Close())
	}
	defer func() {
		if stageRoot != nil {
			resultErr = errors.Join(resultErr, stageRoot.Close())
		}
		if !result.Published {
			current, err := root.Lstat(stageName)
			if err == nil && os.SameFile(identity, current) {
				resultErr = errors.Join(resultErr, root.RemoveAll(stageName), productfiles.SyncDirectory(root))
			}
		}
	}()
	result.Snapshot, err = prepare(stageRoot, filepath.Join(filepath.Dir(destination), stageName))
	if err != nil {
		return result, err
	}
	body, err := json.Marshal(result.Snapshot)
	if err != nil {
		return result, err
	}
	if err := stage.CompareAndPublish(ctx, "snapshot.json", nil, body); err != nil {
		return result, err
	}
	// Windows requires the staging handle to close before directory publication.
	err = stageRoot.Close()
	stageRoot = nil
	if err != nil {
		return result, err
	}
	if _, err := stage.Identity(); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := productfiles.PublishDirectory(root, stageName, root, name); err != nil {
		return result, err
	}
	result.Published = true
	return result, productfiles.SyncDirectory(root)
}

func inspectSQLiteSnapshot(ctx context.Context, root *os.Root, path string) (receipt SQLiteSnapshot, resultErr error) {
	if err := validateSQLiteSnapshot(ctx, filepath.Join(path, "starport.db")); err != nil {
		return receipt, err
	}
	file, err := root.OpenFile("starport.db", os.O_RDWR, 0)
	if err != nil {
		return receipt, err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return receipt, err
	}
	if !info.Mode().IsRegular() {
		return receipt, errors.New("SQLite snapshot is not a regular file")
	}
	hash := sha256.New()
	size, err := io.Copy(hash, &snapshotReader{ctx: ctx, reader: file})
	if err != nil {
		return receipt, err
	}
	if err := file.Sync(); err != nil {
		return receipt, err
	}
	return SQLiteSnapshot{Format: sqliteSnapshotFormat, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

// validateSQLiteSnapshot opens the image without WAL files, writes, or schema migration.
func validateSQLiteSnapshot(ctx context.Context, path string) (resultErr error) {
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(path)+"?mode=ro&immutable=1&_pragma=trusted_schema(0)")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, db.Close()) }()
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return errors.New("SQLite snapshot integrity check failed")
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM pragma_foreign_key_check").Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return errors.New("SQLite snapshot contains invalid references")
	}
	var marker string
	if err := db.QueryRowContext(ctx, "SELECT value FROM sqlstore_meta WHERE name='schema'").Scan(&marker); err != nil {
		return err
	}
	if marker != "starport" {
		return errors.New("SQLite snapshot is not a Starport database")
	}
	names, err := migrationNames(migrations, TypeSQLite)
	if err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, "SELECT name FROM schema_migrations ORDER BY name")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	index := 0
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		if index >= len(names) || name != names[index] {
			return errors.New("SQLite snapshot has unsupported migration history")
		}
		index++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if index == 0 {
		return errors.New("SQLite snapshot has no migration history")
	}
	return nil
}

func copySQLiteSnapshot(ctx context.Context, root *os.Root, source string, expected SQLiteSnapshot) (resultErr error) {
	input, err := os.Open(source) // #nosec G304 -- the operator selects the snapshot path.
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, input.Close()) }()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != expected.Size {
		return errors.New("SQLite snapshot size or file type mismatch")
	}
	output, err := root.OpenFile("starport.db", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, output.Close()) }()
	hash := sha256.New()
	if _, err := io.CopyN(io.MultiWriter(output, hash), &snapshotReader{ctx: ctx, reader: input}, expected.Size); err != nil {
		return err
	}
	var extra [1]byte
	if n, err := input.Read(extra[:]); n != 0 || !errors.Is(err, io.EOF) {
		return fmt.Errorf("SQLite snapshot changed during copy: %w", errors.Join(err, errors.New("unexpected trailing bytes")))
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
		return errors.New("SQLite snapshot digest mismatch")
	}
	return nil
}

type snapshotReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *snapshotReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}
