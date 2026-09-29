package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
)

func copyRestoreTreeFile(ctx context.Context, root *os.Root, source string, file restoreTreeFile) (resultErr error) {
	name := filepath.Join(source, filepath.FromSlash(file.Artifact.Path))
	input, err := productfiles.ExistingDirectory(filepath.Dir(name))
	if err != nil {
		return err
	}
	target := filepath.FromSlash(file.Relative)
	if err := root.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	output, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, output.Close()) }()
	digest := sha256.New()
	copied, err := input.CopyFile(ctx, filepath.Base(name), io.MultiWriter(output, digest), file.Artifact.Size)
	if err != nil {
		return err
	}
	if copied != file.Artifact.Size || hex.EncodeToString(digest.Sum(nil)) != file.Artifact.SHA256 {
		return ErrConflict
	}
	return output.Sync()
}

func validateRestoredTree(ctx context.Context, location string, files []restoreTreeFile, validate FileTreeValidator) (identity string, resultErr error) {
	directory, err := productfiles.ExistingDirectory(location)
	if err != nil {
		return "", err
	}
	root, err := directory.Open()
	if err != nil {
		return "", err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	if err := checkRestoredTree(ctx, root, files); err != nil {
		return "", err
	}
	if err := validate(ctx, location); err != nil {
		return "", err
	}
	// Owner validation must not alter the selection or substitute a different tree.
	if err := checkRestoredTree(ctx, root, files); err != nil {
		return "", err
	}
	if err := syncRestoredTreeFiles(ctx, root, files); err != nil {
		return "", err
	}
	if err := syncBundleDirectories(ctx, root); err != nil {
		return "", err
	}
	return directory.Identity()
}

func checkRestoredTree(ctx context.Context, root *os.Root, files []restoreTreeFile) error {
	expected := make(map[string]BundleArtifact, len(files))
	directories := map[string]bool{".": true}
	for _, file := range files {
		artifact := file.Artifact
		artifact.Path = file.Relative
		expected[file.Relative] = artifact
		for parent := path.Dir(file.Relative); parent != "."; parent = path.Dir(parent) {
			directories[parent] = true
		}
	}
	seen := 0
	err := fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if !directories[name] {
				return ErrConflict
			}
			_, err := productfiles.ExistingDirectory(filepath.Join(root.Name(), filepath.FromSlash(name)))
			return err
		}
		artifact, ok := expected[name]
		if !ok || !entry.Type().IsRegular() {
			return ErrConflict
		}
		actual, err := inspectPrivateTreeFile(ctx, root.Name(), name, artifact.Size)
		if err != nil || actual != artifact {
			return errors.Join(ErrConflict, err)
		}
		seen++
		return nil
	})
	if err != nil {
		return err
	}
	if seen != len(files) {
		return ErrConflict
	}
	return nil
}

func inspectPrivateTreeFile(ctx context.Context, root, name string, limit int64) (BundleArtifact, error) {
	full := filepath.Join(root, filepath.FromSlash(name))
	directory, err := productfiles.ExistingDirectory(filepath.Dir(full))
	if err != nil {
		return BundleArtifact{}, err
	}
	digest := sha256.New()
	copied, err := directory.CopyFile(ctx, filepath.Base(full), digest, limit)
	if err != nil {
		return BundleArtifact{}, err
	}
	return BundleArtifact{Path: name, Size: copied, SHA256: hex.EncodeToString(digest.Sum(nil))}, nil
}

// Reuse must also flush file data. Matching cached bytes alone do not establish durability.
func syncRestoredTreeFiles(ctx context.Context, root *os.Root, files []restoreTreeFile) error {
	for _, entry := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := filepath.FromSlash(entry.Relative)
		before, err := root.Lstat(name)
		if err != nil {
			return err
		}
		if !before.Mode().IsRegular() || before.Size() != entry.Artifact.Size {
			return ErrConflict
		}
		file, err := root.OpenFile(name, os.O_RDWR, 0)
		if err != nil {
			return err
		}
		opened, err := file.Stat()
		if err != nil || !os.SameFile(before, opened) {
			return errors.Join(ErrConflict, err, file.Close())
		}
		if err := file.Sync(); err != nil {
			return errors.Join(err, file.Close())
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	return nil
}
