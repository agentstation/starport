package recovery

import (
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/agentstation/starmap/pkg/productfiles"
)

const preparedBundleFile = "prepared-restore.json"

// stageRestoreFiles publishes inactive files and their completion receipt together.
// The receipt cannot activate stores or overwrite an active configuration.
func stageRestoreFiles(ctx context.Context, destination, source string, manifest BundleManifest, prepared PreparedBundle) (resultErr error) {
	claim, err := json.Marshal(prepared)
	if err != nil {
		return err
	}
	parent, err := productfiles.ExistingDirectory(filepath.Dir(destination))
	if err != nil {
		return err
	}
	parentRoot, err := parent.Open()
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, parentRoot.Close()) }()
	if _, err := parentRoot.Lstat(filepath.Base(destination)); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return err
		}
		directory, err := productfiles.ExistingDirectory(destination)
		if err != nil {
			return err
		}
		root, err := directory.Open()
		if err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
		if err := verifyRestoreFiles(ctx, directory, root, manifest, claim); err != nil {
			return err
		}
		return productfiles.SyncDirectory(parentRoot)
	}
	name := ".restore-files-" + rand.Text()
	stage, err := parent.CreateChild(name)
	if err != nil {
		return err
	}
	root, err := stage.Open()
	if err != nil {
		return err
	}
	identity, err := parentRoot.Lstat(name)
	if err != nil {
		return errors.Join(err, root.Close())
	}
	defer func() {
		if root != nil {
			resultErr = errors.Join(resultErr, root.Close())
		}
		current, err := parentRoot.Lstat(name)
		if err == nil && os.SameFile(identity, current) {
			resultErr = errors.Join(resultErr, parentRoot.RemoveAll(name), productfiles.SyncDirectory(parentRoot))
		}
	}()
	for _, artifact := range manifest.Artifacts {
		id, selected := strings.CutPrefix(artifact.Path, "files/")
		if !selected {
			continue
		}
		file := BundleFile{ID: id, Path: filepath.Join(source, filepath.FromSlash(artifact.Path)), ExpectedSHA256: artifact.SHA256}
		if err := copyBundleFile(ctx, root, file); err != nil {
			return err
		}
	}
	if err := stage.CompareAndPublish(ctx, preparedBundleFile, nil, claim); err != nil {
		return err
	}
	if err := verifyRestoreFiles(ctx, stage, root, manifest, claim); err != nil {
		return err
	}
	if err := syncBundleDirectories(ctx, root); err != nil {
		return err
	}
	// Windows requires the staging handle to close before directory publication.
	err = root.Close()
	root = nil
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := stage.Identity(); err != nil {
		return err
	}
	if err := productfiles.PublishDirectory(parentRoot, name, parentRoot, filepath.Base(destination)); err != nil {
		return err
	}
	return productfiles.SyncDirectory(parentRoot)
}

func verifyRestoreFiles(ctx context.Context, directory *productfiles.Directory, root *os.Root, manifest BundleManifest, claim []byte) error {
	if err := verifyPreparedReceipt(directory, claim); err != nil {
		return err
	}
	expected := make(map[string]BundleArtifact)
	directories := map[string]bool{".": true, ".record-publications": true}
	for _, artifact := range manifest.Artifacts {
		if !strings.HasPrefix(artifact.Path, "files/") {
			continue
		}
		expected[artifact.Path] = artifact
		for name := path.Dir(artifact.Path); name != "."; name = path.Dir(name) {
			directories[name] = true
		}
	}
	count := 0
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
			return nil
		}
		if !entry.Type().IsRegular() {
			return ErrConflict
		}
		switch name {
		case preparedBundleFile:
			return nil
		case ".record-publications/.owner.lock":
			info, err := entry.Info()
			if err != nil || info.Size() != 0 {
				return errors.Join(ErrConflict, err)
			}
			return nil
		}
		artifact, ok := expected[name]
		if !ok {
			return ErrConflict
		}
		actual, err := inspectBundleArtifact(ctx, root, name)
		if err != nil || actual != artifact {
			return errors.Join(ErrConflict, err)
		}
		count++
		return nil
	})
	if err != nil {
		return err
	}
	if count != len(expected) {
		return ErrConflict
	}
	if err := verifyPreparedReceipt(directory, claim); err != nil {
		return err
	}
	_, err = directory.Identity()
	return err
}

func verifyPreparedReceipt(directory *productfiles.Directory, claim []byte) error {
	body, err := directory.ReadFile(preparedBundleFile, int64(len(claim)+1))
	if err != nil || string(body) != string(claim) {
		return errors.Join(ErrConflict, err)
	}
	return nil
}
