package recovery

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/sqlstore"
)

type historyProjectionOwners struct {
	directory string
	kv        *KVSnapshotView
	sql       *sqlstore.DB
	sqlParent *os.Root
	sqlID     fs.FileInfo
	blobs     *blob.ArchiveProjection
}

func openHistoryProjectionOwners(ctx context.Context, source *RestoreSource, directory string) (_ *historyProjectionOwners, resultErr error) {
	owners := &historyProjectionOwners{directory: directory}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, owners.close())
		}
	}()
	view, err := OpenKVSnapshot(ctx, filepath.Join(source.request.Directory, filepath.FromSlash(bundleKVFile)), directory, source.manifest.KV)
	if err != nil {
		return nil, err
	}
	owners.kv = view
	if err := view.db.Close(); err != nil {
		view.db = nil
		return nil, err
	}
	// Only this already verified private copy changes. Historical expiration stays intact.
	view.db, err = openKVSnapshot(KVSnapshotPath(filepath.Join(directory, view.name)), false)
	if err != nil {
		return nil, err
	}
	view.db.SetMaxOpenConns(2)
	result, err := sqlstore.RestoreSQLiteSnapshot(ctx, filepath.Join(directory, "working-sql"), filepath.Join(source.request.Directory, filepath.FromSlash(bundleSQLFile)), source.manifest.SQL)
	if err != nil || !result.Published {
		return nil, errors.Join(ErrConflict, err)
	}
	parent, err := productfiles.ExistingDirectory(directory)
	if err != nil {
		return nil, err
	}
	owners.sqlParent, err = parent.Open()
	if err != nil {
		return nil, err
	}
	owners.sqlID, err = owners.sqlParent.Lstat("working-sql")
	if err != nil {
		return nil, err
	}
	owners.sql, err = sqlstore.Open(sqlstore.Config{Type: sqlstore.TypeSQLite, SQLite: sqlstore.SQLiteConfig{Path: filepath.Join(directory, "working-sql", "starport.db")}})
	if err != nil {
		return nil, err
	}
	owners.blobs, err = blob.OpenArchiveProjection(ctx, filepath.Join(source.request.Directory, bundleBlobFile), directory, source.manifest.Blobs)
	if err != nil {
		return nil, err
	}
	return owners, nil
}

func (o *historyProjectionOwners) close() error {
	var result error
	if o.kv != nil {
		result = errors.Join(result, o.kv.Close())
		o.kv = nil
	}
	if o.sql != nil {
		result = errors.Join(result, o.sql.Close())
		o.sql = nil
	}
	if o.sqlParent != nil {
		current, err := o.sqlParent.Lstat("working-sql")
		if err == nil && (o.sqlID == nil || !os.SameFile(o.sqlID, current)) {
			err = ErrConflict
		}
		if err == nil {
			err = errors.Join(o.sqlParent.RemoveAll("working-sql"), productfiles.SyncDirectory(o.sqlParent))
		}
		result = errors.Join(result, err, o.sqlParent.Close())
		o.sqlParent = nil
	}
	if o.blobs != nil {
		result = errors.Join(result, o.blobs.Close())
		o.blobs = nil
	}
	return result
}

func (o *historyProjectionOwners) apply(ctx context.Context, step historyStep, payload []byte, history *verifiedHistoryState, through time.Time, encryption *credentials.EncryptionService) (resultErr error) {
	switch historyNativeOwner(step.Kind) {
	case historyOwnerKV:
		if step.Kind == historyKVAuthorityFinal {
			return ErrConflict
		}
		prepared, err := prepareHistoryKV(ctx, step.Kind, payload, o.kv, o.blobs, through, encryption, revision.RecoveryAuthority{})
		if err != nil {
			return err
		}
		return applyHistoryProjectionKV(ctx, o.kv, prepared)
	case historyOwnerSQL:
		if step.Kind != historySQLIdentity {
			return ErrConflict
		}
		prepared, err := prepareHistorySQL(step.Kind, payload, revision.RecoveryAuthority{})
		if err != nil {
			return err
		}
		conn, err := o.sql.Conn(ctx)
		if err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, conn.Close()) }()
		if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			return err
		}
		defer func() {
			rollback, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = conn.ExecContext(rollback, "ROLLBACK")
		}()
		if err := prepared.apply(ctx, o.sql, conn); err != nil {
			return err
		}
		_, err = conn.ExecContext(ctx, "COMMIT")
		return err
	case historyOwnerBlob:
		return o.applyBlob(ctx, payload, history)
	default:
		return ErrConflict
	}
}

func applyHistoryProjectionKV(ctx context.Context, view *KVSnapshotView, prepared preparedHistoryKV) error {
	if ctx == nil || view == nil || view.db == nil || !historyDigest(prepared.digest) || len(prepared.mutations) == 0 || len(prepared.mutations) > typedHistoryMaxMutations {
		return ErrConflict
	}
	tx, err := view.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, change := range prepared.mutations {
		var value []byte
		var expires int64
		err := tx.QueryRowContext(ctx, "SELECT value,expires FROM records WHERE key=?", []byte(change.Key)).Scan(&value, &expires)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		present := err == nil
		if present != (change.ExpectedValue != nil) || present && (!bytes.Equal(value, change.ExpectedValue) || expires != 0) || change.TTL != 0 {
			return ErrConflict
		}
		if change.NewValue == nil {
			_, err = tx.ExecContext(ctx, "DELETE FROM records WHERE key=?", []byte(change.Key))
		} else {
			_, err = tx.ExecContext(ctx, "INSERT INTO records(key,value,expires) VALUES(?,?,0) ON CONFLICT(key) DO UPDATE SET value=excluded.value,expires=excluded.expires", []byte(change.Key), change.NewValue)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (o *historyProjectionOwners) applyBlob(ctx context.Context, payload []byte, history *verifiedHistoryState) (resultErr error) {
	var input historyBlobPayload
	if _, err := decodeHistoryPayload("blob_publication", payload, &input); err != nil || input.Version != 1 || !explicitHistoryMembers(payload, "expected", "next", "asset_id") {
		return errors.Join(ErrConflict, err)
	}
	var stream io.Reader
	var staged *os.File
	if input.Next.Kind == historyProjectionBlobLive {
		found := false
		for _, asset := range history.manifest.Assets {
			found = found || asset.ID == input.AssetID && asset.Size == input.Next.Size && asset.SHA256 == input.Next.SHA256
		}
		if !found {
			return ErrConflict
		}
		var err error
		staged, err = os.CreateTemp(o.directory, ".projection-asset-")
		if err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, staged.Close(), os.Remove(staged.Name())) }()
		if err := copyHistoryAsset(ctx, history, input.AssetID, staged); err != nil {
			return err
		}
		if _, err := staged.Seek(0, io.SeekStart); err != nil {
			return err
		}
		stream = staged
	} else if input.AssetID != "" {
		return ErrConflict
	}
	return o.blobs.ApplyPublication(ctx, input.Key, input.Expected, input.Next, stream)
}

func (o *historyProjectionOwners) snapshot(ctx context.Context, binding string, stop int) (historyProjectionRecord, error) {
	result := historyProjectionRecord{Version: 1, BindingSHA256: binding, PrefixSteps: stop}
	var err error
	result.KV, err = SnapshotKV(ctx, o.kv, filepath.Join(o.directory, historyProjectionKVDirectory))
	if err != nil {
		return result, err
	}
	sqlResult, err := o.sql.SnapshotSQLite(ctx, filepath.Join(o.directory, "result-sql"))
	if err != nil || !sqlResult.Published {
		return result, errors.Join(ErrConflict, err)
	}
	result.SQL = sqlResult.Snapshot
	view, err := sqlstore.OpenRelationalSnapshot(ctx, filepath.Join(o.directory, "result-sql", "starport.db"), result.SQL, o.directory)
	if err != nil {
		return result, err
	}
	result.SQLCensus, err = view.RecoveryCensus(ctx)
	if err = errors.Join(err, view.Close()); err != nil {
		return result, err
	}
	result.Blobs, err = o.blobs.Snapshot(ctx, filepath.Join(o.directory, "result-blobs.tar"))
	return result, err
}
