package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
)

// ImportInspector captures imported bytes while their startup barrier remains closed.
// Callers must fence writers and discard partial output after any error.
type ImportInspector interface {
	SnapshotImport(context.Context, string, string, Snapshot) (Snapshot, error)
	CheckImport(context.Context, string, Snapshot) error
}

func checkBlobImport(ctx context.Context, records activationRecords, operation string, original Snapshot) error {
	if ctx == nil {
		return ErrImportRestricted
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	claim, err := makeBlobClaim(operation, original)
	if err != nil {
		return err
	}
	data, err := records.read(ctx, blobImportKey)
	if err != nil || !bytes.Equal(data, claim) {
		return errors.Join(ErrImportRestricted, err)
	}
	digest := sha256.Sum256(claim)
	for _, key := range []string{blobActivationCurrent, blobActivationPrefix + hex.EncodeToString(digest[:])} {
		if _, err := records.read(ctx, key); !errors.Is(err, ErrNotFound) {
			return errors.Join(ErrImportRestricted, err)
		}
	}
	return ctx.Err()
}

func (t filesystemRestoreTarget) CheckImport(ctx context.Context, operation string, original Snapshot) error {
	if ctx == nil {
		return ErrImportRestricted
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	directory, err := productfiles.ExistingDirectory(t.destination)
	if err != nil {
		return err
	}
	control, err := directory.ExistingChild(".starport")
	if err != nil {
		return err
	}
	if err := control.CheckNoPendingPublications(ctx); err != nil {
		return err
	}
	return checkBlobImport(ctx, filesystemActivation{directory: control}, operation, original)
}

func (t objectRestoreTarget) CheckImport(ctx context.Context, operation string, original Snapshot) error {
	if t.store == nil || ctx == nil {
		return ErrImportRestricted
	}
	if err := checkBlobImport(ctx, objectActivation(t), operation, original); err != nil {
		return err
	}
	if err := t.store.readLayout(ctx); err != nil && !isAbsent(err) {
		return err
	}
	return ctx.Err()
}

func (t filesystemRestoreTarget) SnapshotImport(ctx context.Context, destination, operation string, original Snapshot) (Snapshot, error) {
	guard := func(ctx context.Context) error { return t.CheckImport(ctx, operation, original) }
	if err := guard(ctx); err != nil {
		return Snapshot{}, err
	}
	if err := checkInspectionDestination(t.destination, destination); err != nil {
		return Snapshot{}, err
	}
	source := &Filesystem{root: t.destination}
	walk := func(ctx context.Context, yield blobObjectVisitor) error {
		return source.walkObjectsChecked(ctx, guard, yield)
	}
	return snapshotBlobImport(ctx, destination, walk, guard)
}

func checkInspectionDestination(source, destination string) error {
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return errors.New("blob: inspection requires a clean absolute destination")
	}
	identity, err := os.Stat(source)
	if err != nil {
		return err
	}
	for parent := filepath.Dir(destination); ; parent = filepath.Dir(parent) {
		current, err := os.Stat(parent)
		if err != nil {
			return err
		}
		if os.SameFile(identity, current) {
			return errors.New("blob: inspection destination overlaps imported storage")
		}
		if parent == filepath.Dir(parent) {
			return nil
		}
	}
}

func (t objectRestoreTarget) SnapshotImport(ctx context.Context, destination, operation string, original Snapshot) (Snapshot, error) {
	guard := func(ctx context.Context) error { return t.CheckImport(ctx, operation, original) }
	if err := guard(ctx); err != nil {
		return Snapshot{}, err
	}
	walk := func(ctx context.Context, yield blobObjectVisitor) error {
		return t.store.walkObjects(ctx, false, yield)
	}
	return snapshotBlobImport(ctx, destination, walk, guard)
}

func snapshotBlobImport(ctx context.Context, destination string, walk blobObjectWalker, guard func(context.Context) error) (Snapshot, error) {
	receipt, err := writeBlobSnapshot(ctx, destination, func(ctx context.Context, yield blobObjectVisitor) error {
		if err := guard(ctx); err != nil {
			return err
		}
		return walk(ctx, func(address string, size int64, input io.Reader) error {
			if err := guard(ctx); err != nil {
				return err
			}
			return yield(address, size, input)
		})
	})
	if err != nil {
		return Snapshot{}, err
	}
	if err := guard(ctx); err != nil {
		return Snapshot{}, err
	}
	return receipt, nil
}

var (
	_ ImportInspector = filesystemRestoreTarget{}
	_ ImportInspector = objectRestoreTarget{}
)
