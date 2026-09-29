package blob

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentstation/starmap/pkg/productfiles"
)

type filesystemActivation struct{ directory *productfiles.Directory }

func (t filesystemRestoreTarget) ActivateImport(ctx context.Context, operation string, expected Snapshot, decisionSHA256 string) error {
	return t.ActivateImportAt(ctx, operation, expected, ImportReplayPosition{}, decisionSHA256)
}
func (t filesystemRestoreTarget) ActivateImportAt(ctx context.Context, operation string, expected Snapshot, position ImportReplayPosition, decisionSHA256 string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	claim, err := makeBlobClaim(operation, expected)
	if err != nil {
		return err
	}
	if !activationDigest(decisionSHA256) {
		return ErrActivationConflict
	}
	root, err := productfiles.ExistingDirectory(t.destination)
	if err != nil {
		return err
	}
	control, err := root.ExistingChild(".starport")
	if err != nil {
		return err
	}
	records := filesystemActivation{directory: control}
	if err := checkReplayPosition(ctx, records, claim, position); err != nil {
		return err
	}
	return activateBlobImport(ctx, records, claim, decisionSHA256)
}

func (f filesystemActivation) read(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value, err := f.directory.ReadFile(strings.TrimPrefix(name, ".starport/"), 4096)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return value, err
}

func (f filesystemActivation) compare(ctx context.Context, name string, previous, value []byte) error {
	existing, err := f.read(ctx, name)
	if err == nil && bytes.Equal(existing, value) {
		return f.confirm(ctx)
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if err := f.directory.CompareAndPublish(ctx, strings.TrimPrefix(name, ".starport/"), previous, value); err != nil {
		return err
	}
	return nil
}

func (f filesystemActivation) prepare(ctx context.Context) error { return ctx.Err() }
func (f filesystemActivation) confirm(ctx context.Context) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := f.directory.RecoverPublications(ctx); err != nil {
		return err
	}
	if err := f.directory.CheckNoPendingPublications(ctx); err != nil {
		return err
	}
	root, err := f.directory.Open()
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	return productfiles.SyncDirectory(root)
}

// backupControlEntry excludes native activation authority and completed journal
// controls. Portable history remains a validated archive entry.
func backupControlEntry(address string, info os.FileInfo) (bool, error) {
	switch address {
	case blobActivationCurrent, blobImportKey, blobReplayCurrent:
		if !info.Mode().IsRegular() {
			return false, ErrActivationConflict
		}
		return true, nil
	case ".starport/.record-publications/.owner.lock":
		if !info.Mode().IsRegular() || info.Size() != 0 {
			return false, ErrActivationConflict
		}
		return true, nil
	default:
		return false, nil
	}
}

var _ ImportActivator = filesystemRestoreTarget{}

func checkFilesystemActivation(ctx context.Context, path string) error {
	if _, err := os.Lstat(filepath.Join(path, ".starport")); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	root, err := productfiles.ExistingDirectory(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	control, err := root.ExistingChild(".starport")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return checkActivationBarrier(ctx, filesystemActivation{directory: control})
}
