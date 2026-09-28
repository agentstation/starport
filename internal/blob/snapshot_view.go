package blob

import (
	"context"
	"errors"
	"io"
)

// PublicationReader reads retained bytes without exposing publication or retirement.
type PublicationReader interface {
	ReadPublished(context.Context, string) (io.ReadCloser, error)
	StatPublished(context.Context, string) (Info, error)
}

// SnapshotView owns a verified private archive copy. It exposes no writes.
type SnapshotView interface {
	PublicationReader
	Get(context.Context, string) (io.ReadCloser, error)
	Stat(context.Context, string) (Info, error)
	Close() error
}

type snapshotView struct {
	image *blobImage
	store *Filesystem
}

// OpenSnapshot verifies and extracts a private copy for offline reference checks.
// The caller must close it to remove the temporary copy.
func OpenSnapshot(ctx context.Context, source, scratch string, expected Snapshot) (SnapshotView, error) {
	image, err := prepareBlobImage(ctx, scratch, source, expected)
	if err != nil {
		return nil, err
	}
	store, err := NewFilesystem(image.path)
	if err != nil {
		return nil, errors.Join(err, image.close())
	}
	return &snapshotView{image: image, store: store}, nil
}

func (s *snapshotView) ReadPublished(ctx context.Context, key string) (io.ReadCloser, error) {
	return s.store.ReadPublished(ctx, key)
}
func (s *snapshotView) StatPublished(ctx context.Context, key string) (Info, error) {
	return s.store.StatPublished(ctx, key)
}
func (s *snapshotView) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	return s.store.Get(ctx, key)
}
func (s *snapshotView) Stat(ctx context.Context, key string) (Info, error) {
	return s.store.Stat(ctx, key)
}
func (s *snapshotView) Close() error { return s.image.close() }
