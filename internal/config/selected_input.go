package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starmap/pkg/productpaths"
	"github.com/agentstation/starmap/pkg/productpaths/policy"
)

const recoveryInputMaxBytes = 1 << 20

type recoveryFileBinding struct {
	Role     string `json:"role"`
	Path     string `json:"path"`
	Resolved string `json:"resolved"`
	Identity string `json:"identity"`
	SHA256   string `json:"sha256"`
	Access   string `json:"access"`
}

func readRecoveryFile(ctx context.Context, entry productpaths.FileEntry) ([]byte, recoveryFileBinding, error) {
	var binding recoveryFileBinding
	maxBytes := int64(recoveryInputMaxBytes)
	if entry.ID == fileRoleSourceFile {
		maxBytes = catalogs.MaxCatalogPayloadBytes
	}
	if err := ctx.Err(); err != nil {
		return nil, binding, err
	}
	path := entry.Location.Path
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, binding, errors.New("recovery input requires a clean absolute path")
	}
	if err := productfiles.ValidateAncestors(path); err != nil {
		return nil, binding, err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, binding, err
	}
	if err := productfiles.ValidateAncestors(resolved); err != nil {
		return nil, binding, err
	}
	root, err := os.OpenRoot(filepath.Dir(resolved))
	if err != nil {
		return nil, binding, err
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(filepath.Base(resolved))
	if err != nil {
		return nil, binding, err
	}
	defer func() { _ = file.Close() }()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() > maxBytes {
		return nil, binding, errors.New("recovery input exceeds its regular-file bound")
	}
	native, err := productfiles.FileIdentity(file)
	if err != nil {
		return nil, binding, err
	}
	var body []byte
	switch entry.Policy.Access {
	case policy.OwnerOnly:
		body, err = productpaths.ReadDotenv(ctx, path, maxBytes)
	case policy.ServiceManaged:
		body, err = productpaths.ReadConfiguration(ctx, productpaths.ConfigurationInput{Path: path, AccessPolicy: policy.ServiceManaged, Explicit: true, MaxBytes: maxBytes})
	case policy.DeploymentControlled:
		body, err = io.ReadAll(io.LimitReader(file, maxBytes+1))
	default:
		return nil, binding, errors.New("recovery input has an unsupported access policy")
	}
	if err != nil || int64(len(body)) > maxBytes {
		return nil, binding, errors.New("recovery input could not be read within its bound")
	}
	after, err := file.Stat()
	current, statErr := root.Lstat(filepath.Base(resolved))
	selected, selectedErr := filepath.EvalSymlinks(path)
	if err != nil || statErr != nil || selectedErr != nil || selected != resolved || !os.SameFile(before, after) || !os.SameFile(before, current) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, binding, errors.New("recovery input changed during its read")
	}
	digest := sha256.Sum256(body)
	binding = recoveryFileBinding{Role: entry.ID, Path: path, Resolved: resolved, Identity: native, SHA256: hex.EncodeToString(digest[:]), Access: entry.Policy.Access}
	return body, binding, nil
}
