package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/setup"
)

func topologyTargetRole(role string) bool {
	return role == "workspace" || strings.HasPrefix(role, "workspace-") || role == "catalog-migration-lock" || strings.HasPrefix(role, "setup-") || role == "welcome-stamp"
}

type topologyTargetFile struct {
	Name, Identity, SHA256 string
	Size                   int64
}
type topologyTargetInputs struct {
	Workspace            string
	RootIdentity         string
	Files                []topologyTargetFile
	Setup                []byte
	Welcome              *topologyTargetFile
	AcquisitionDirectory string
	AcquisitionIdentity  string
	AcquisitionFiles     []topologyTargetFile
}

func inspectTopologyTargetInputs(ctx context.Context, cfg *config.Config) ([]byte, error) {
	record := topologyTargetInputs{Workspace: cfg.Catalog.WorkspacePath}
	setupRecord, err := setup.InspectRecoveryState(ctx, cfg.EffectivePaths())
	if err != nil {
		return nil, err
	}
	record.Setup = setupRecord
	record.AcquisitionDirectory = cfg.CatalogCredentialPolicyDirectory()
	record.AcquisitionIdentity, record.AcquisitionFiles, err = inspectTopologyTree(ctx, record.AcquisitionDirectory, func(*os.Root) error {
		return catalogSettings(cfg).InspectCredentialPolicy(ctx, record.AcquisitionDirectory)
	}, true)
	if err != nil {
		return nil, err
	}

	if record.Workspace != "" {
		identity, files, err := inspectTopologyTree(ctx, record.Workspace, func(root *os.Root) error {
			loaded, err := catalogs.NewFromFS(root.FS(), ".")
			if err != nil {
				return err
			}
			return loaded.LoadReport().Err()
		}, true)
		if err != nil {
			return nil, err
		}
		record.RootIdentity, record.Files = identity, files
	}

	stamp := cfg.EffectivePaths().WelcomeStampFile
	if stamp != "" {
		root, err := os.OpenRoot(filepath.Dir(stamp))
		if !errors.Is(err, os.ErrNotExist) && err != nil {
			return nil, err
		}
		if err == nil {
			binding, readErr := inspectTopologyFile(ctx, root, filepath.Base(stamp))
			closeErr := root.Close()
			if !errors.Is(readErr, os.ErrNotExist) && readErr != nil {
				return nil, readErr
			}
			if closeErr != nil {
				return nil, closeErr
			}
			if readErr == nil {
				if binding.Size > 512 {
					return nil, recovery.ErrConflict
				}
				record.Welcome = &binding
			}
		}
	}
	return json.Marshal(record, json.Deterministic(true))
}
func inspectTopologyFile(ctx context.Context, root *os.Root, name string) (topologyTargetFile, error) {
	var binding topologyTargetFile
	before, err := root.Lstat(name)
	if err != nil {
		return binding, err
	}
	if !before.Mode().IsRegular() || before.Size() < 0 || before.Size() > 64<<20 {
		return binding, recovery.ErrConflict
	}
	file, err := root.Open(name)
	if err != nil {
		return binding, err
	}
	defer func() { _ = file.Close() }()
	identity, err := productfiles.FileIdentity(file)
	if err != nil {
		return binding, err
	}
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(file, before.Size()+1))
	if err != nil || size != before.Size() {
		return binding, errors.Join(recovery.ErrConflict, err)
	}
	after, err := root.Lstat(name)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return binding, recovery.ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return binding, err
	}
	return topologyTargetFile{name, identity, hex.EncodeToString(hash.Sum(nil)), size}, nil
}

// inspectTopologyTree bounds the complete native census around owner semantic validation.
func inspectTopologyTree(ctx context.Context, path string, validate func(*os.Root) error, allowAbsent bool) (string, []topologyTargetFile, error) {
	before, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && allowAbsent {
		return "absent", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return "", nil, recovery.ErrConflict
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = root.Close() }()
	handle, err := root.Open(".")
	if err != nil {
		return "", nil, err
	}
	identity, err := productfiles.FileIdentity(handle)
	closeErr := handle.Close()
	if err = errors.Join(err, closeErr); err != nil {
		return "", nil, err
	}
	files, err := inspectTopologyCensus(ctx, root)
	if err != nil {
		return "", nil, err
	}
	if err := validate(root); err != nil {
		return "", nil, err
	}
	again, err := inspectTopologyCensus(ctx, root)
	if err != nil || !slices.Equal(files, again) {
		return "", nil, errors.Join(recovery.ErrConflict, err)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		return "", nil, recovery.ErrConflict
	}
	return identity, files, nil
}
func inspectTopologyCensus(ctx context.Context, root *os.Root) ([]topologyTargetFile, error) {
	var files []topologyTargetFile
	pending := []string{"."}
	count := 0
	var total int64
	for len(pending) > 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		listing, err := root.Open(name)
		if err != nil {
			return nil, err
		}
		for {
			entries, readErr := listing.ReadDir(64)
			for _, entry := range entries {
				count++
				if count > 4096 {
					_ = listing.Close()
					return nil, recovery.ErrConflict
				}
				if entry.Type()&os.ModeSymlink != 0 {
					_ = listing.Close()
					return nil, recovery.ErrConflict
				}
				child := entry.Name()
				if name != "." {
					child = name + "/" + child
				}
				if entry.IsDir() {
					pending = append(pending, child)
					continue
				}
				binding, err := inspectTopologyFile(ctx, root, child)
				if err != nil {
					_ = listing.Close()
					return nil, err
				}
				total += binding.Size
				if total > 64<<20 {
					_ = listing.Close()
					return nil, recovery.ErrConflict
				}
				files = append(files, binding)
			}
			if readErr != nil {
				closeErr := listing.Close()
				if !errors.Is(readErr, io.EOF) {
					return nil, errors.Join(readErr, closeErr)
				}
				if closeErr != nil {
					return nil, closeErr
				}
				break
			}
			if err := ctx.Err(); err != nil {
				_ = listing.Close()
				return nil, err
			}
		}
	}
	slices.SortFunc(files, func(a, b topologyTargetFile) int { return strings.Compare(a.Name, b.Name) })
	return files, nil
}
