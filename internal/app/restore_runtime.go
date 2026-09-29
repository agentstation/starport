package app

import (
	"context"
	"errors"

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
	retained, read, err := retainedRestoreInputs(source, tree)
	if err != nil {
		return tree, nil, err
	}
	inactive, err := catalogSettings(cfg).InspectRetainedMigration(ctx, original, read)
	if err != nil {
		return tree, nil, err
	}
	publications, err := catalogSettings(cfg).InspectRetainedPublications(ctx, retained, read)
	if err != nil {
		return tree, nil, err
	}
	tree, remaining, err = selectInactiveRestoreFiles(tree, plan, remaining, config.RuntimeEvidenceRole, inactive, "verified-history", "Completed migration records retain historical path identities. Preserve them in the verified backup and inactive preparation.")
	if err != nil {
		return tree, nil, err
	}
	return selectInactiveRestoreFiles(tree, plan, remaining, config.RuntimeEvidenceRole, publications, "verified-staging", retainedStagingReason)
}
