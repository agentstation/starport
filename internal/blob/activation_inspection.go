package blob

import (
	"bytes"
	"context"
	"errors"
	"github.com/agentstation/starmap/pkg/productfiles"
)

// ImportActivationInspector verifies an exact native release without repair or publication.
// Its result establishes historical component completion, not current admission permission.
type ImportActivationInspector interface {
	CheckActivatedImportAt(context.Context, string, Snapshot, ImportReplayPosition, string) error
}

func inspectBlobActivation(ctx context.Context, records activationRecords, operation string, original Snapshot, position ImportReplayPosition, decision string) error {
	if ctx == nil {
		return ErrActivationConflict
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	claim, err := makeBlobClaim(operation, original)
	if err != nil {
		return err
	}
	key, history, _, active, err := makeActivation(claim, decision)
	if err != nil {
		return err
	}
	if err := checkReplayPosition(ctx, records, claim, position); err != nil {
		return err
	}
	for name, expected := range map[string][]byte{key: history, blobImportKey: active, blobActivationCurrent: active} {
		actual, err := records.read(ctx, name)
		if err != nil || !bytes.Equal(actual, expected) {
			return errors.Join(ErrActivationConflict, err)
		}
	}
	if err := checkAdoptionClosure(ctx, records); err != nil {
		return errors.Join(ErrActivationConflict, err)
	}
	return ctx.Err()
}

func (t filesystemRestoreTarget) CheckActivatedImportAt(ctx context.Context, operation string, original Snapshot, position ImportReplayPosition, decision string) error {
	root, err := productfiles.ExistingDirectory(t.destination)
	if err != nil {
		return err
	}
	control, err := root.ExistingChild(".starport")
	if err != nil {
		return err
	}
	if err := control.CheckNoPendingPublications(ctx); err != nil {
		return err
	}
	return inspectBlobActivation(ctx, filesystemActivation{directory: control}, operation, original, position, decision)
}
func (t objectRestoreTarget) CheckActivatedImportAt(ctx context.Context, operation string, original Snapshot, position ImportReplayPosition, decision string) error {
	if t.store == nil {
		return ErrActivationConflict
	}
	return inspectBlobActivation(ctx, objectActivation(t), operation, original, position, decision)
}

// ImportUnreleasedInspector refuses any activation attempt before application preparation.
// It allows an existing replay cursor while requiring the original closed import claim.
type ImportUnreleasedInspector interface {
	CheckUnreleasedImport(context.Context, string, Snapshot) error
}

func checkUnreleasedBlobImport(ctx context.Context, records activationRecords, operation string, original Snapshot) error {
	return checkBlobImport(ctx, records, operation, original)
}
func (t filesystemRestoreTarget) CheckUnreleasedImport(ctx context.Context, operation string, original Snapshot) error {
	root, err := productfiles.ExistingDirectory(t.destination)
	if err != nil {
		return err
	}
	control, err := root.ExistingChild(".starport")
	if err != nil {
		return err
	}
	if err := control.CheckNoPendingPublications(ctx); err != nil {
		return err
	}
	return checkUnreleasedBlobImport(ctx, filesystemActivation{directory: control}, operation, original)
}
func (t objectRestoreTarget) CheckUnreleasedImport(ctx context.Context, operation string, original Snapshot) error {
	if t.store == nil {
		return ErrActivationConflict
	}
	return checkUnreleasedBlobImport(ctx, objectActivation(t), operation, original)
}
