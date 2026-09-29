package recovery

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/agentstation/starmap/pkg/productfiles"
)

// FileTreeFile maps one verified payload to a relative name chosen by its owner.
type FileTreeFile struct {
	ArtifactID string `json:"artifact_id"`
	Relative   string `json:"relative"`
}

// FileTreeRequest selects one complete private tree and an explicit target directory.
// The caller must fence source and target writers and select paths through the product owner.
type FileTreeRequest struct {
	Destination string         `json:"destination"`
	Files       []FileTreeFile `json:"files"`
}

// FileTreeResult reports namespace publication or verified reuse of identical existing content.
// Neither result approves admission, runtime identity reuse, or external fencing.
type FileTreeResult struct {
	Destination       string `json:"destination"`
	DirectoryIdentity string `json:"directory_identity"`
	SelectionSHA256   string `json:"selection_sha256"`
	Files             int    `json:"files"`
	Published         bool   `json:"published"`
	Reused            bool   `json:"reused"`
}

// FileTreeValidator checks component semantics without changing files or starting a service.
// The validator must accept both a private staging directory and an existing target.
type FileTreeValidator func(context.Context, string) error

type restoreTreeFile struct {
	FileTreeFile
	Artifact BundleArtifact `json:"artifact"`
}

// PublishFileTree installs a complete verified tree without replacing existing files.
// The required owner check precedes publication. Every retry verifies existing content again.
// An error after publication can return Published=true. Admission must remain closed on any error.
func (s *RestoreSource) PublishFileTree(ctx context.Context, request FileTreeRequest, validate FileTreeValidator) (result FileTreeResult, resultErr error) {
	return s.publishFileTree(ctx, request, validate, nil)
}

func (s *RestoreSource) publishFileTree(ctx context.Context, request FileTreeRequest, validate FileTreeValidator, checkpoint func(string) error) (result FileTreeResult, resultErr error) {
	if ctx == nil || validate == nil {
		return result, errors.New("file restore requires context and an owner validator")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	files, digest, err := s.prepareFileTree(request)
	if err != nil {
		return result, err
	}
	result = FileTreeResult{Destination: request.Destination, SelectionSHA256: digest, Files: len(files)}
	parent, err := productfiles.ExistingDirectory(filepath.Dir(request.Destination))
	if err != nil {
		return result, err
	}
	parentRoot, err := parent.Open()
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, parentRoot.Close()) }()
	if _, err := parentRoot.Lstat(filepath.Base(request.Destination)); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return result, err
		}
		identity, err := validateRestoredTree(ctx, request.Destination, files, validate)
		if err != nil {
			return result, err
		}
		if _, err := parent.Identity(); err != nil {
			return result, err
		}
		result.DirectoryIdentity, result.Reused = identity, true
		return result, productfiles.SyncDirectory(parentRoot)
	}
	name := ".restore-tree-" + rand.Text()
	stage, err := parent.CreateChild(name)
	if err != nil {
		return result, err
	}
	root, err := stage.Open()
	if err != nil {
		return result, err
	}
	identity, err := parentRoot.Lstat(name)
	if err != nil {
		return result, errors.Join(err, root.Close())
	}
	defer func() {
		resultErr = errors.Join(resultErr, root.Close())
		current, err := parentRoot.Lstat(name)
		if err == nil && os.SameFile(identity, current) {
			resultErr = errors.Join(resultErr, parentRoot.RemoveAll(name), productfiles.SyncDirectory(parentRoot))
		}
	}()
	for _, file := range files {
		if err := copyRestoreTreeFile(ctx, root, s.request.Directory, file); err != nil {
			return result, err
		}
	}
	stagePath := filepath.Join(filepath.Dir(request.Destination), name)
	nativeIdentity, err := validateRestoredTree(ctx, stagePath, files, validate)
	if err != nil {
		return result, err
	}
	if checkpoint != nil {
		if err := checkpoint("validated"); err != nil {
			return result, err
		}
	}
	if current, err := stage.Identity(); err != nil || current != nativeIdentity {
		return result, errors.Join(ErrConflict, err)
	}
	if _, err := parent.Identity(); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := productfiles.PublishDirectory(parentRoot, name, parentRoot, filepath.Base(request.Destination)); err != nil {
		return result, err
	}
	result.Published, result.DirectoryIdentity = true, nativeIdentity
	if checkpoint != nil {
		if err := checkpoint("published"); err != nil {
			return result, err
		}
	}
	return result, confirmFileTreePublication(parentRoot, request.Destination, nativeIdentity)
}

// Confirm durability and identity after the target becomes visible.
func confirmFileTreePublication(parent *os.Root, destination, identity string) error {
	if err := productfiles.SyncDirectory(parent); err != nil {
		return err
	}
	published, err := productfiles.ExistingDirectory(destination)
	if err != nil {
		return err
	}
	currentIdentity, err := published.Identity()
	if err != nil || currentIdentity != identity {
		return errors.Join(ErrConflict, err)
	}
	return nil
}

func (s *RestoreSource) prepareFileTree(request FileTreeRequest) ([]restoreTreeFile, string, error) {
	if s == nil || s.manifest.Format != bundleFormat {
		return nil, "", errors.New("file restore requires a verified source")
	}
	if len(request.Files) == 0 || len(request.Files) > bundleMaxArtifacts {
		return nil, "", errors.New("file restore requires a bounded nonempty inventory")
	}
	if err := CheckRestoreDestinations(s.request.Directory, s.request.ScratchDirectory, request.Destination); err != nil {
		return nil, "", err
	}
	artifacts := make(map[string]BundleArtifact, len(s.manifest.Artifacts))
	for _, artifact := range s.manifest.Artifacts {
		artifacts[artifact.Path] = artifact
	}
	seenIDs, seenNames := make(map[string]bool), make(map[string]bool)
	files := make([]restoreTreeFile, 0, len(request.Files))
	for _, file := range request.Files {
		if !validBundlePath(file.ArtifactID) || !validRestoreTreeName(file.Relative) {
			return nil, "", errors.New("file restore contains an unsafe selection")
		}
		artifact, ok := artifacts["files/"+file.ArtifactID]
		name := strings.ToLower(file.Relative)
		if !ok || seenIDs[file.ArtifactID] || seenNames[name] {
			return nil, "", errors.New("file restore contains a duplicate or unknown selection")
		}
		seenIDs[file.ArtifactID], seenNames[name] = true, true
		files = append(files, restoreTreeFile{FileTreeFile: file, Artifact: artifact})
	}
	for name := range seenNames {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if seenNames[parent] {
				return nil, "", errors.New("file restore paths conflict")
			}
		}
	}
	slices.SortFunc(files, func(a, b restoreTreeFile) int { return strings.Compare(a.Relative, b.Relative) })
	selection, err := json.Marshal(struct {
		Manifest    string            `json:"manifest"`
		Destination string            `json:"destination"`
		Files       []restoreTreeFile `json:"files"`
	}{s.request.ManifestSHA256, request.Destination, files})
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(selection)
	return files, hex.EncodeToString(digest[:]), nil
}

func validRestoreTreeName(name string) bool {
	if name == "." || !fs.ValidPath(name) || len(name) > 4096 || strings.ContainsAny(name, "\\:*?\"<>|") || strings.ContainsFunc(name, unicode.IsControl) {
		return false
	}
	if _, err := filepath.Localize(name); err != nil {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
		stem := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if slices.Contains([]string{"CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9"}, stem) {
			return false
		}
	}
	return true
}
