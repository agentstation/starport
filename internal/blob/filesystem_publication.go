package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentstation/starmap/pkg/productfiles"
)

func (f *Filesystem) publicationPath(key string) string {
	return f.namespacedPath(retainedDir, key)
}

// Publish stages durable bytes, then links them without replacing an identity.
// A retirement marker occupies that same identity and rejects a delayed link.
func (f *Filesystem) Publish(ctx context.Context, key string, r io.Reader) (Info, error) {
	return f.writePublication(ctx, key, r, false)
}

// Retire replaces either live bytes or an absent identity with a durable marker.
func (f *Filesystem) Retire(ctx context.Context, key string) error {
	_, err := f.writePublication(ctx, key, strings.NewReader(""), true)
	return err
}

func (f *Filesystem) writePublication(ctx context.Context, key string, r io.Reader, retire bool) (Info, error) {
	if err := ValidateKey(key); err != nil {
		return Info{}, err
	}
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	target := f.publicationPath(key)
	if err := os.MkdirAll(filepath.Dir(target), dirPerm); err != nil {
		return Info{}, err
	}
	staged, err := os.CreateTemp(filepath.Join(f.root, stagingDir), "publication-")
	if err != nil {
		return Info{}, err
	}
	defer func() { _ = staged.Close(); _ = os.Remove(staged.Name()) }()
	if err := staged.Chmod(filePerm); err != nil {
		return Info{}, err
	}
	header := liveEnvelope
	if retire {
		header = retiredEnvelope
	}
	if _, err := io.WriteString(staged, header); err != nil {
		return Info{}, err
	}
	size, err := io.Copy(staged, &contextReader{ctx: ctx, r: r})
	if err != nil {
		return Info{}, err
	}
	if err := staged.Sync(); err != nil {
		return Info{}, err
	}
	if err := staged.Close(); err != nil {
		return Info{}, err
	}
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	if retire {
		err = retryBlockedReplace(ctx, func() error { return f.placePublication(staged.Name(), target, true) })
	} else {
		err = f.placePublication(staged.Name(), target, false)
	}
	if errors.Is(err, fs.ErrExist) {
		return Info{}, ErrPublicationExists
	}
	if err != nil {
		return Info{}, fmt.Errorf("blob: publish retained identity: %w", err)
	}
	// Flush the entry and its ancestors through the configured root.
	// Construction already flushed any newly created root ancestors.
	for dir := filepath.Dir(target); ; dir = filepath.Dir(dir) {
		if err := syncPublicationDirectory(dir); err != nil {
			return Info{}, err
		}
		if dir == f.root {
			break
		}
	}
	return Info{Key: key, Size: size}, nil
}

// placePublication links new bytes or renames a retirement marker over the
// identity. It holds the store lock only for that one directory change.
func (f *Filesystem) placePublication(staged, target string, retire bool) error {
	f.publishing.Lock()
	defer f.publishing.Unlock()
	if retire {
		return os.Rename(staged, target)
	}
	return os.Link(staged, target)
}

func syncPublicationDirectory(path string) error {
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return productfiles.SyncDirectory(root)
}

// ReadPublished consumes the private envelope before exposing payload bytes.
func (f *Filesystem) ReadPublished(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(f.publicationPath(key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := readEnvelope(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

// StatPublished uses one open handle so retirement cannot mix two versions.
func (f *Filesystem) StatPublished(ctx context.Context, key string) (Info, error) {
	reader, err := f.ReadPublished(ctx, key)
	if err != nil {
		return Info{}, err
	}
	defer func() { _ = reader.Close() }()
	info, err := reader.(*os.File).Stat()
	if err != nil {
		return Info{}, err
	}
	return Info{Key: key, Size: info.Size() - int64(len(liveEnvelope))}, nil
}
