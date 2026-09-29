package blob

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"io/fs"
	"os"

	"github.com/agentstation/starmap/pkg/productfiles"
)

func verifyFilesystemImport(ctx context.Context, destination string, image *blobImage, claim []byte, expected int64) (resultErr error) {
	directory, err := productfiles.ExistingDirectory(destination)
	if err != nil {
		return err
	}
	root, err := directory.Open()
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	control, err := productfiles.ExistingDirectory(destination + string(os.PathSeparator) + ".starport")
	if err != nil {
		return err
	}
	if err := verifyFilesystemImportReceipt(control, claim); err != nil {
		return err
	}
	var count int64
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		if entry.IsDir() {
			if name == ".starport" || name == ".starport/.record-publications" {
				return nil
			}
			expectedInfo, err := image.root.Lstat(name)
			if err != nil || !expectedInfo.IsDir() {
				return errors.Join(ErrImportRestricted, err)
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return ErrImportRestricted
		}
		if name == blobImportKey {
			return nil
		}
		if name == ".starport/.record-publications/.owner.lock" {
			info, err := entry.Info()
			if err != nil || info.Size() != 0 {
				return errors.Join(ErrImportRestricted, err)
			}
			return nil
		}
		if !validBlobAddress(name) {
			return ErrImportRestricted
		}
		expectedInfo, err := image.root.Lstat(name)
		if err != nil || !expectedInfo.Mode().IsRegular() {
			return errors.Join(ErrImportRestricted, err)
		}
		original, err := hashRestoreFile(ctx, image.root, name, expectedInfo.Size())
		if err != nil {
			return err
		}
		restored, err := hashRestoreFile(ctx, root, name, expectedInfo.Size())
		if err != nil {
			return err
		}
		if original != restored {
			return ErrImportRestricted
		}
		count++
		return nil
	})
	if err != nil {
		return err
	}
	if count != expected {
		return ErrImportRestricted
	}
	// Verify the receipt again after reading the complete target.
	if err := verifyFilesystemImportReceipt(control, claim); err != nil {
		return err
	}
	_, err = directory.Identity()
	return err
}

func verifyFilesystemImportReceipt(control *productfiles.Directory, claim []byte) error {
	if _, err := control.ReadFile("activation-current", blobActivationLimit); !errors.Is(err, os.ErrNotExist) {
		return errors.Join(ErrImportRestricted, err)
	}
	value, err := control.ReadFile("import", int64(len(claim)+1))
	if err != nil || string(value) != string(claim) {
		return errors.Join(ErrImportRestricted, err)
	}
	return nil
}

func hashRestoreFile(ctx context.Context, root *os.Root, name string, size int64) (sum [32]byte, resultErr error) {
	file, err := root.Open(name)
	if err != nil {
		return sum, err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != size {
		return sum, errors.Join(ErrImportRestricted, err)
	}
	digest := sha256.New()
	if _, err := io.CopyN(digest, &contextReader{ctx: ctx, r: file}, size); err != nil {
		return sum, err
	}
	var extra [1]byte
	if n, err := file.Read(extra[:]); n != 0 || !errors.Is(err, io.EOF) {
		return sum, ErrImportRestricted
	}
	copy(sum[:], digest.Sum(nil))
	return sum, nil
}
