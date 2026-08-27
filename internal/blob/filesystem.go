package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	// filesystemBackend is the name an operator reads at startup.
	filesystemBackend = "filesystem"

	// objectsDir holds the reachable objects, and stagingDir holds a put that
	// has not finished. They are siblings under the root so that the final
	// rename stays inside one filesystem and therefore stays atomic.
	objectsDir = "objects"
	stagingDir = "staging"

	dirPerm  fs.FileMode = 0o700
	filePerm fs.FileMode = 0o600
)

// Filesystem stores objects as files under a configured root directory.
//
// It suits one node. A deployment that runs more than one Starport process
// against the same files needs the object store backend instead, which FIL2
// implements behind this same contract.
type Filesystem struct {
	root string
}

// NewFilesystem opens a filesystem store rooted at the directory. It creates
// the root and its two subdirectories when they are absent, and it refuses a
// root it cannot write.
func NewFilesystem(root string) (*Filesystem, error) {
	if root == "" {
		return nil, errors.New("blob: the filesystem root is empty")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("blob: resolve the filesystem root: %w", err)
	}
	for _, dir := range []string{objectsDir, stagingDir} {
		if err := os.MkdirAll(filepath.Join(absolute, dir), dirPerm); err != nil {
			return nil, fmt.Errorf("blob: create the filesystem root: %w", err)
		}
	}
	return &Filesystem{root: absolute}, nil
}

// Backend implements Store.
func (f *Filesystem) Backend() string { return filesystemBackend }

// objectPath maps a key to the file that holds its bytes.
//
// The file is named by the hash of the key rather than by the key itself. A
// key that this package accepts is not always a name every filesystem accepts,
// and the hash removes that whole class of difference between one host and the
// next. The hash also shards the tree, so no directory grows one entry per
// stored file.
func (f *Filesystem) objectPath(key string) string {
	sum := sha256.Sum256([]byte(key))
	name := hex.EncodeToString(sum[:])
	return filepath.Join(f.root, objectsDir, name[0:2], name[2:4], name)
}

// Put implements Store.
func (f *Filesystem) Put(ctx context.Context, key string, r io.Reader) (Info, error) {
	if err := ValidateKey(key); err != nil {
		return Info{}, err
	}
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}

	target := f.objectPath(key)
	if err := os.MkdirAll(filepath.Dir(target), dirPerm); err != nil {
		return Info{}, fmt.Errorf("blob: create the object directory: %w", err)
	}

	staged, err := os.CreateTemp(filepath.Join(f.root, stagingDir), "put-")
	if err != nil {
		return Info{}, fmt.Errorf("blob: stage the object: %w", err)
	}
	stagedName := staged.Name()
	// A staged file that never reaches the rename is removed here, so a failed
	// put leaves nothing behind at the key and nothing behind in staging.
	committed := false
	defer func() {
		if !committed {
			_ = staged.Close()
			_ = os.Remove(stagedName)
		}
	}()

	if err := staged.Chmod(filePerm); err != nil {
		return Info{}, fmt.Errorf("blob: set the object mode: %w", err)
	}
	size, err := io.Copy(staged, &contextReader{ctx: ctx, r: r})
	if err != nil {
		return Info{}, fmt.Errorf("blob: write the object: %w", err)
	}
	// The bytes reach the medium before the rename makes them reachable, so a
	// crash cannot leave a name that points at an incomplete object.
	if err := staged.Sync(); err != nil {
		return Info{}, fmt.Errorf("blob: flush the object: %w", err)
	}
	if err := staged.Close(); err != nil {
		return Info{}, fmt.Errorf("blob: close the object: %w", err)
	}
	if err := os.Rename(stagedName, target); err != nil {
		return Info{}, fmt.Errorf("blob: commit the object: %w", err)
	}
	committed = true

	return Info{Key: key, Size: size}, nil
}

// Get implements Store.
func (f *Filesystem) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// #nosec G304 -- the path comes from objectPath, which names a file by the
	// hash of a validated key under the configured root.
	file, err := os.Open(f.objectPath(key))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, key)
		}
		return nil, fmt.Errorf("blob: open the object: %w", err)
	}
	return file, nil
}

// Stat implements Store.
func (f *Filesystem) Stat(ctx context.Context, key string) (Info, error) {
	if err := ValidateKey(key); err != nil {
		return Info{}, err
	}
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	info, err := os.Stat(f.objectPath(key))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Info{}, fmt.Errorf("%w: %s", ErrNotFound, key)
		}
		return Info{}, fmt.Errorf("blob: stat the object: %w", err)
	}
	return Info{Key: key, Size: info.Size()}, nil
}

// Delete implements Store.
func (f *Filesystem) Delete(ctx context.Context, key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Remove(f.objectPath(key)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("blob: delete the object: %w", err)
	}
	return nil
}

// contextReader stops a copy when the caller cancels. A large upload otherwise
// runs to completion after the request that asked for it is gone.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *contextReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
