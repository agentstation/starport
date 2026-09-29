package blob

import (
	"archive/tar"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
)

type blobImage struct {
	parent    *os.Root
	root      *os.Root
	directory *productfiles.Directory
	name      string
	path      string
	identity  fs.FileInfo
}

func (s Snapshot) validate() error {
	digest, err := hex.DecodeString(s.SHA256)
	if err != nil || len(digest) != sha256.Size || s.Format != blobSnapshotFormat || s.Size < 1024 || s.Size%512 != 0 || s.Objects < 0 || s.Retired < 0 || s.Retired > s.Objects {
		return errors.New("blob: invalid snapshot identity")
	}
	return nil
}

func prepareBlobImage(ctx context.Context, scratch, source string, expected Snapshot) (image *blobImage, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := expected.validate(); err != nil {
		return nil, err
	}
	parent, err := productfiles.ExistingDirectory(scratch)
	if err != nil {
		return nil, err
	}
	root, err := parent.Open()
	if err != nil {
		return nil, err
	}
	name := ".blob-restore-" + rand.Text()
	directory, err := parent.CreateChild(name)
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	stage, err := directory.Open()
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	identity, err := root.Lstat(name)
	if err != nil {
		return nil, errors.Join(err, stage.Close(), root.Close())
	}
	prepared := &blobImage{parent: root, root: stage, directory: directory, name: name, path: filepath.Join(scratch, name), identity: identity}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, prepared.close())
		}
	}()
	if err := prepared.copy(ctx, source, expected); err != nil {
		return nil, err
	}
	if err := prepared.extract(ctx, expected); err != nil {
		return nil, err
	}
	if err := stage.Remove("archive.tar"); err != nil {
		return nil, err
	}
	if err := productfiles.SyncDirectory(stage); err != nil {
		return nil, err
	}
	return prepared, nil
}

func (i *blobImage) closeRoot() error {
	if i.root == nil {
		return nil
	}
	err := i.root.Close()
	i.root = nil
	return err
}

func (i *blobImage) close() error {
	err := i.closeRoot()
	current, statErr := i.parent.Lstat(i.name)
	if statErr == nil && os.SameFile(current, i.identity) {
		err = errors.Join(err, i.parent.RemoveAll(i.name), productfiles.SyncDirectory(i.parent))
	}
	return errors.Join(err, i.parent.Close())
}

func (i *blobImage) copy(ctx context.Context, source string, expected Snapshot) (resultErr error) {
	input, err := os.Open(source) // #nosec G304 -- the operator supplies the archive. The digest binds its bytes before extraction.
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, input.Close()) }()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != expected.Size {
		return errors.New("blob: snapshot size or file type mismatch")
	}
	output, err := i.root.OpenFile("archive.tar", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, output.Close()) }()
	hash := sha256.New()
	if _, err := io.CopyN(io.MultiWriter(output, hash), &contextReader{ctx: ctx, r: input}, expected.Size); err != nil {
		return err
	}
	var extra [1]byte
	if n, err := input.Read(extra[:]); n != 0 || !errors.Is(err, io.EOF) {
		return errors.New("blob: snapshot changed during copy")
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
		return errors.New("blob: snapshot digest mismatch")
	}
	return output.Sync()
}

func (i *blobImage) extract(ctx context.Context, expected Snapshot) (resultErr error) {
	file, err := i.root.Open("archive.tar")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	reader := tar.NewReader(&contextReader{ctx: ctx, r: file})
	var objects, retired, occupied int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg || header.Format != tar.FormatUSTAR || !validBlobAddress(header.Name) || header.Size < 0 || header.Size > expected.Size-1024 {
			return errors.New("blob: unsupported snapshot entry")
		}
		blocks := header.Size / 512
		if header.Size%512 != 0 {
			blocks++
		}
		if blocks > (expected.Size-1024-occupied)/512-1 {
			return errors.New("blob: snapshot entry exceeds its archive")
		}
		occupied += (blocks + 1) * 512
		if objects >= expected.Objects {
			return errors.New("blob: snapshot contains more objects than its receipt")
		}
		wasRetired, err := i.extractObject(ctx, header, reader)
		if err != nil {
			return err
		}
		objects++
		if wasRetired {
			retired++
		}
	}
	if occupied+1024 != expected.Size || objects != expected.Objects || retired != expected.Retired {
		return errors.New("blob: snapshot counts or trailer mismatch")
	}
	for _, name := range []string{objectsDir, stagingDir, retainedDir} {
		if err := i.root.MkdirAll(name, 0o700); err != nil {
			return err
		}
	}
	return fs.WalkDir(i.root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		root, err := i.root.OpenRoot(name)
		if err != nil {
			return err
		}
		return errors.Join(productfiles.SyncDirectory(root), root.Close())
	})
}

func (i *blobImage) extractObject(ctx context.Context, header *tar.Header, input io.Reader) (retired bool, resultErr error) {
	reader, retired, err := inspectBlobEnvelope(header.Name, header.Size, input)
	if err != nil {
		return false, err
	}
	if err := i.root.MkdirAll(filepath.Dir(header.Name), 0o700); err != nil {
		return false, err
	}
	file, err := i.root.OpenFile(header.Name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return false, err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	if _, err := io.CopyN(file, &contextReader{ctx: ctx, r: reader}, header.Size); err != nil {
		return false, err
	}
	return retired, file.Sync()
}
