package app

import (
	"context"
	"slices"

	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
)

func selectBaselineRestoreFiles(ctx context.Context, source *recovery.RestoreSource, tree recovery.FileTreeRequest, plan, remaining []recovery.FileDisposition) (recovery.FileTreeRequest, []recovery.FileDisposition, error) {
	inventory := tree
	inventory.Files = slices.Clone(tree.Files)
	for _, file := range plan {
		if file.Role == config.BaselineRecoveryRole {
			inventory.Files = append(inventory.Files, recovery.FileTreeFile{ArtifactID: file.ArtifactID, Relative: catalog.BaselineRecoveryDirectoryName + "/" + file.Relative})
		}
	}
	files, read, err := retainedRestoreInputs(source, inventory)
	if err != nil {
		return tree, nil, err
	}
	inactive, err := catalog.InspectRetainedBaselinePublications(ctx, files, read)
	if err != nil {
		return tree, nil, err
	}
	tree, remaining, err = selectInactiveRestoreFiles(tree, plan, remaining, config.BaselineRole, inactive, "verified-staging", retainedStagingReason)
	if err != nil {
		return tree, nil, err
	}
	for index := range remaining {
		if remaining[index].Role == config.BaselineRecoveryRole {
			remaining[index].Destination = ""
			remaining[index].Action = "verified-history"
			remaining[index].Reason = "Verified baseline publication records retain historical native identities. Keep them in the backup and inactive preparation."
		}
	}
	return tree, remaining, nil
}
