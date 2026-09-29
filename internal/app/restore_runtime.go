package app

import (
	"context"
	"errors"
	"os"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
)

func selectRuntimeRestoreFiles(ctx context.Context, cfg *config.Config, source *recovery.RestoreSource, tree recovery.FileTreeRequest, plan, remaining []recovery.FileDisposition) (recovery.FileTreeRequest, []recovery.FileDisposition, error) {
	inventory, err := readBackupInventory(ctx, source)
	if err != nil {
		return tree, nil, err
	}
	original := ""
	for _, role := range inventory.Roles {
		if role.Entry.ID == config.RuntimeEvidenceRole {
			original = role.Entry.Location.Path
		}
	}
	if original == "" {
		return tree, nil, errors.New("runtime recovery requires its captured directory identity")
	}
	files := make(map[string]string, len(tree.Files))
	for _, file := range tree.Files {
		files[file.Relative] = file.ArtifactID
	}
	inactive, err := catalogSettings(cfg).InspectRetainedMigration(ctx, original, func(ctx context.Context, name string, limit int64) ([]byte, error) {
		id, found := files[name]
		if !found {
			return nil, os.ErrNotExist
		}
		return source.SelectedFile(ctx, id, limit)
	})
	if err != nil {
		return tree, nil, err
	}
	keepInactive := make(map[string]bool, len(inactive))
	for _, name := range inactive {
		if _, exists := files[name]; !exists || keepInactive[name] {
			return tree, nil, errors.New("runtime owner selected unknown or duplicate historical records")
		}
		keepInactive[name] = true
	}
	selected := make([]recovery.FileTreeFile, 0, len(tree.Files)-len(inactive))
	for _, file := range tree.Files {
		if !keepInactive[file.Relative] {
			selected = append(selected, file)
		}
	}
	for _, file := range plan {
		if file.Role == config.RuntimeEvidenceRole && keepInactive[file.Relative] {
			file.Destination, file.Action = "", "verified-history"
			file.Reason = "Completed migration records retain historical path identities. Preserve them in the verified backup and inactive preparation."
			remaining = append(remaining, file)
		}
	}
	tree.Files = selected
	return tree, remaining, nil
}
