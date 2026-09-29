package blob

import (
	"context"
	"errors"
	"path/filepath"
)

// RestoreTarget retains restricted bytes for an explicit deployment restore.
// A successful call grants no permission to start the gateway.
type RestoreTarget interface {
	Restore(context.Context, string, string, string, Snapshot) error
}

type filesystemRestoreTarget struct{ destination string }
type objectRestoreTarget struct{ store *ObjectStore }

// FilesystemRestoreTarget selects a new directory or an exact prior import.
func FilesystemRestoreTarget(destination string) (RestoreTarget, error) {
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return nil, errors.New("blob: restore requires a clean absolute destination")
	}
	return filesystemRestoreTarget{destination: destination}, nil
}

// ObjectRestoreTarget selects an empty namespace or an exact prior import.
func ObjectRestoreTarget(store *ObjectStore) (RestoreTarget, error) {
	if store == nil {
		return nil, errors.New("blob: restore requires object storage")
	}
	return objectRestoreTarget{store: store}, nil
}

func (t filesystemRestoreTarget) Restore(ctx context.Context, source, _, operation string, expected Snapshot) error {
	_, err := RestoreFilesystemOnce(ctx, t.destination, source, operation, expected)
	return err
}

func (t objectRestoreTarget) Restore(ctx context.Context, source, scratch, operation string, expected Snapshot) error {
	return RestoreObjectStore(ctx, t.store, source, scratch, operation, expected)
}

// RestoreFilesystemOnce verifies an exact prior import before accepting its retry.
func RestoreFilesystemOnce(ctx context.Context, destination, source, operation string, expected Snapshot) (RestoreResult, error) {
	return restoreFilesystem(ctx, destination, source, operation, expected, true)
}
