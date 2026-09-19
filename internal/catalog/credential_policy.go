package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agentstation/starmap/acquisition"
	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/storage"
)

// credentialPolicy classifies retained state before catalog startup creates its markers.
func (s Settings) credentialPolicy(ctx context.Context, store storage.KVStore) (*acquisition.CredentialPolicyState, error) {
	if s.CredentialPolicyDirectory == "" {
		return nil, nil
	}
	legacy, err := s.HasRetainedInstallation(ctx, store)
	if err != nil {
		return nil, err
	}
	owner := s.directoryOwner()
	return &acquisition.CredentialPolicyState{Directory: s.CredentialPolicyDirectory, Product: owner.Product, DeploymentID: owner.Deployment, InstanceID: owner.Instance, LegacyInstallation: legacy}, nil
}

// HasRetainedInstallation checks catalog markers before startup creates new state.
func (s Settings) HasRetainedInstallation(ctx context.Context, store storage.KVStore) (bool, error) {
	if ctx == nil {
		return false, fmt.Errorf("catalog credential policy requires a context")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	legacy := false
	for _, marker := range []struct {
		path      string
		directory bool
	}{
		{path: childCredentialMarker(s.StateDirectory, "instance-seed")},
		{path: s.BaselineDirectory, directory: true},
	} {
		if marker.path == "" {
			continue
		}
		if err := productfiles.ValidateAncestors(marker.path); err != nil {
			return false, fmt.Errorf("inspect catalog credential policy marker: %w", err)
		}
		info, err := os.Lstat(marker.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("inspect catalog credential policy marker: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || info.IsDir() != marker.directory || !marker.directory && !info.Mode().IsRegular() {
			return false, fmt.Errorf("catalog credential policy marker has an invalid file type")
		}
		legacy = true
	}
	if store == nil {
		return false, fmt.Errorf("catalog credential policy requires deployment storage")
	}
	for _, key := range []string{catalogCurrentGenerationKey, candidateCurrentGenerationKey} {
		_, err := store.Get(ctx, key)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("inspect retained catalog policy state: %w", err)
		}
		legacy = true
	}
	return legacy, nil
}

func childCredentialMarker(parent, name string) string {
	if parent == "" {
		return ""
	}
	return filepath.Join(parent, name)
}
