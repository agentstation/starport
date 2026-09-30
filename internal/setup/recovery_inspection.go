package setup

import (
	"context"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/config"
)

// InspectRecoveryState checks current target setup ownership without creating or repairing state.
// Captured source setup transactions remain inactive and never become target instructions.
func InspectRecoveryState(ctx context.Context, paths config.Paths) ([]byte, error) {
	if ctx == nil {
		return nil, ErrPartialState
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateSetupPaths(paths); err != nil {
		return nil, err
	}
	type binding struct{ Path, Identity string }
	records := []binding{}
	for _, path := range []string{filepath.Join(filepath.Dir(paths.ConfigFile), setupMetadataDirectory), filepath.Join(filepath.Dir(paths.ConfigFile), recordPublicationDirectory), filepath.Join(filepath.Dir(paths.BadgerDir), databaseLockDirectory(paths))} {
		directory, err := productfiles.ExistingDirectory(path)
		if errors.Is(err, os.ErrNotExist) {
			records = append(records, binding{path, "absent"})
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := directory.CheckNoPendingPublications(ctx); err != nil {
			return nil, err
		}
		if err := inspectSettledMetadata(directory, filepath.Base(path) != recordPublicationDirectory); err != nil {
			return nil, err
		}
		identity, err := directory.Identity()
		if err != nil {
			return nil, err
		}
		records = append(records, binding{path, identity})
	}
	parent, err := productfiles.ExistingDirectory(filepath.Dir(paths.BadgerDir))
	if errors.Is(err, os.ErrNotExist) {
		return json.Marshal(records, json.Deterministic(true))
	}
	if err != nil {
		return nil, err
	}
	entries, err := setupEntries(parent)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".starport-init-") {
			return nil, ErrPartialState
		}
	}
	return json.Marshal(records, json.Deterministic(true))
}
