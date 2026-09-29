package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/dgraph-io/badger/v4"
)

const badgerBackupFormat = "badger-v4-full-v1"

// BadgerBackup binds one complete export to its exact bytes.
// The deployment manifest must retain this record independently of the payload.
type BadgerBackup struct {
	Format string `json:"format"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Backup exports committed records without replacing an existing backup file.
// This snapshot covers only Badger. The deployment coordinator owns cross-store consistency.
func (s *BadgerStore) Backup(ctx context.Context, path string) (receipt BadgerBackup, resultErr error) {
	if err := ctx.Err(); err != nil {
		return receipt, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return receipt, ErrStorageClosed
	}
	parent, err := productfiles.ExistingDirectory(filepath.Dir(path))
	if err != nil {
		return receipt, err
	}
	root, err := parent.Open()
	if err != nil {
		return receipt, err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	file, err := root.OpenFile(filepath.Base(path), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return receipt, err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	hash := sha256.New()
	writer := &badgerBackupWriter{ctx: ctx, writer: io.MultiWriter(file, hash)}
	if _, err := s.db.Backup(writer, 0); err != nil {
		return receipt, err
	}
	if err := ctx.Err(); err != nil {
		return receipt, err
	}
	if err := file.Sync(); err != nil {
		return receipt, err
	}
	if err := productfiles.SyncDirectory(root); err != nil {
		return receipt, err
	}
	return BadgerBackup{Format: badgerBackupFormat, Size: writer.size, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

type badgerBackupWriter struct {
	ctx    context.Context
	writer io.Writer
	size   int64
}

func (w *badgerBackupWriter) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := w.writer.Write(data)
	w.size += int64(n)
	return n, err
}

// BadgerRestoreResult reports whether the candidate reached its destination.
// A durability error after publication can return Published with an error.
type BadgerRestoreResult struct {
	Published bool
}

// RestoreBadger validates a backup and publishes a new persistent database.
// Existing destinations remain untouched. The caller must not admit traffic before deployment recovery completes.
func RestoreBadger(ctx context.Context, config BadgerConfig, source string, expected BadgerBackup) (result BadgerRestoreResult, resultErr error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	digest, err := hex.DecodeString(expected.SHA256)
	if err != nil || len(digest) != sha256.Size || expected.Size < 0 || expected.Format != badgerBackupFormat {
		return result, errors.New("invalid Badger backup identity")
	}
	if config.InMemory || !filepath.IsAbs(config.Path) || filepath.Clean(config.Path) != config.Path {
		return result, errors.New("badger restore requires a clean absolute persistent destination")
	}
	parent, err := productfiles.ExistingDirectory(filepath.Dir(config.Path))
	if err != nil {
		return result, err
	}
	root, err := parent.Open()
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	name := filepath.Base(config.Path)
	if _, err := root.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		return result, errors.Join(os.ErrExist, err)
	}
	stageName := ".badger-restore-" + rand.Text()
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
		_ = stageRoot.Close()
		return result, err
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
	stagedConfig := config
	stagedConfig.Path = filepath.Join(filepath.Dir(config.Path), stageName)
	if err := loadBadgerBackup(ctx, stageRoot, stagedConfig, source, expected); err != nil {
		return result, err
	}
	if err := stageRoot.Remove("restore-input"); err != nil {
		return result, err
	}
	if err := syncBadgerRestore(ctx, stageRoot); err != nil {
		return result, err
	}
	body, err := json.Marshal(expected)
	if err != nil {
		return result, err
	}
	if err := stage.CompareAndPublish(ctx, "restore-receipt.json", nil, body); err != nil {
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

func copyBadgerBackup(ctx context.Context, destination *os.File, source string, expected BadgerBackup) (resultErr error) {
	input, err := os.Open(source) // #nosec G304 -- the operator selects the backup path.
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, input.Close()) }()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != expected.Size {
		return errors.New("badger backup size or file type mismatch")
	}
	hash := sha256.New()
	if _, err := io.CopyN(io.MultiWriter(destination, hash), &badgerBackupReader{ctx: ctx, reader: input}, expected.Size); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
		return errors.New("badger backup digest mismatch")
	}
	_, err = destination.Seek(0, io.SeekStart)
	return err
}

// validateBadgerBackupFrames refuses truncated lengths before Badger allocates a frame.
func validateBadgerBackupFrames(ctx context.Context, file *os.File, size int64) error {
	remaining := size
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		if remaining < 8 {
			return io.ErrUnexpectedEOF
		}
		var length uint64
		if err := binary.Read(file, binary.LittleEndian, &length); err != nil {
			return err
		}
		remaining -= 8
		if length > uint64(remaining) {
			return io.ErrUnexpectedEOF
		}
		if _, err := file.Seek(int64(length), io.SeekCurrent); err != nil {
			return err
		} // #nosec G115 -- length does not exceed the nonnegative remaining size.
		remaining -= int64(length) // #nosec G115 -- length does not exceed remaining.
	}
	_, err := file.Seek(0, io.SeekStart)
	return err
}

type badgerBackupReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *badgerBackupReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}

func syncBadgerRestore(ctx context.Context, root *os.Root) error {
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unexpected restored entry %q", entry.Name())
		}
		file, err := root.OpenFile(entry.Name(), os.O_RDWR, 0)
		if err != nil {
			return err
		}
		if err := errors.Join(file.Sync(), file.Close()); err != nil {
			return err
		}
	}
	return productfiles.SyncDirectory(root)
}

func loadBadgerBackup(ctx context.Context, stageRoot *os.Root, config BadgerConfig, source string, expected BadgerBackup) error {
	payload, err := stageRoot.OpenFile("restore-input", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := copyBadgerBackup(ctx, payload, source, expected); err != nil {
		return errors.Join(err, payload.Close())
	}
	if err := validateBadgerBackupFrames(ctx, payload, expected.Size); err != nil {
		return errors.Join(err, payload.Close())
	}
	if config.GCInterval == 0 {
		config.GCInterval = 5 * time.Minute
	}
	if config.GCDiscardRatio == 0 {
		config.GCDiscardRatio = 0.5
	}
	opts, err := badgerEngineOptions(config, false)
	if err != nil {
		return errors.Join(err, payload.Close())
	}
	db, err := badger.Open(opts)
	if err != nil {
		return errors.Join(err, payload.Close())
	}
	loadErr := db.Load(&badgerBackupReader{ctx: ctx, reader: payload}, 16)
	if err := errors.Join(loadErr, db.Close(), payload.Close()); err != nil {
		return err
	}
	return nil
}
