package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/gofrs/flock"
)

const (
	recordPublicationDirectory = ".record-publications"
	setupMetadataDirectory     = ".starport-setup"
	setupLockFile              = ".owner.lock"
	setupJournalFile           = "transaction.json"
)

type setupWriter struct {
	configuration *productfiles.Directory
	directory     *productfiles.Directory
	lock          *flock.Flock
	identity      os.FileInfo
}

func acquireSetupWriter(ctx context.Context, configDirectory *productfiles.Directory) (_ *setupWriter, resultErr error) {
	return acquireSetupLock(ctx, configDirectory, setupMetadataDirectory)
}

func acquireSetupLock(ctx context.Context, configDirectory *productfiles.Directory, name string) (_ *setupWriter, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	metadata, err := configDirectory.Child(name)
	if err != nil {
		return nil, err
	}
	parent, err := configDirectory.Open()
	if err != nil {
		return nil, err
	}
	if err := errors.Join(productfiles.SyncDirectory(parent), parent.Close()); err != nil {
		return nil, err
	}
	return lockSetupMetadata(ctx, configDirectory, metadata)
}

func lockSetupMetadata(ctx context.Context, configDirectory, metadata *productfiles.Directory) (_ *setupWriter, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := metadata.ReadFile(setupLockFile, 0); os.IsNotExist(err) {
		if err := metadata.CompareAndPublish(ctx, setupLockFile, nil, []byte{}); err != nil {
			return nil, errors.Join(ErrPartialState, err)
		}
	} else if err != nil {
		return nil, err
	}
	root, err := metadata.Open()
	if err != nil {
		return nil, err
	}
	before, statErr := root.Lstat(setupLockFile)
	lockPath := filepath.Join(root.Name(), setupLockFile)
	if err := errors.Join(statErr, root.Close()); err != nil {
		return nil, err
	}
	writer := &setupWriter{configuration: configDirectory, directory: metadata, identity: before,
		lock: flock.New(lockPath, flock.SetFlag(os.O_RDWR))}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, writer.close())
		}
	}()
	locked, err := writer.lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, fmt.Errorf("%w: another process owns local setup", ErrPartialState)
	}
	return writer, writer.check(ctx)
}

func (w *setupWriter) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if w == nil || w.lock == nil || w.identity == nil {
		return ErrPartialState
	}
	configuration, err := w.configuration.Open()
	if err != nil {
		return err
	}
	if err := configuration.Close(); err != nil {
		return err
	}
	root, err := w.directory.Open()
	if err != nil {
		return err
	}
	current, statErr := root.Lstat(setupLockFile)
	if err := errors.Join(statErr, root.Close()); err != nil {
		return err
	}
	held, err := w.lock.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(w.identity, current) || !os.SameFile(w.identity, held) {
		return fmt.Errorf("%w: setup writer identity changed", ErrPartialState)
	}
	_, err = w.directory.ReadFile(setupLockFile, 0)
	return err
}

func (w *setupWriter) close() error {
	if w == nil || w.lock == nil {
		return nil
	}
	err := w.lock.Close()
	w.lock = nil
	return err
}

func (w *setupWriter) Close() error { return w.close() }
