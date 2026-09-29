package app

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/recovery"
)

// PublishBackupFiles publishes one supported role at its configured canonical location.
// It verifies or resumes restricted preparation first. All writers must remain fenced.
// A publication error can leave a complete tree. Exact retries verify it without replacement.
func PublishBackupFiles(ctx context.Context, cfg *config.Config, request recovery.PublishFilesRequest) (result recovery.PublishFilesResult, err error) {
	if err := request.Validate(); err != nil {
		return result, err
	}
	if request.Role != config.InferenceCredentialPolicyRole && request.Role != config.AcquisitionPolicyRole {
		return result, errors.New("this file role requires a separate owner recovery procedure")
	}
	source, plan, targets, err := inspectBackupRestore(ctx, cfg, request.PrepareRequest)
	if err != nil {
		return result, err
	}
	tree, remaining, err := credentialPolicyRestoreSelection(cfg, request.Role, plan)
	if err != nil {
		return result, err
	}
	scratch := request.ScratchDirectory
	if scratch == "" {
		scratch = filepath.Dir(request.Directory)
	}
	if err := recovery.CheckRestoreDestinations(request.Directory, scratch, append(targets, tree.Destination)...); err != nil {
		return result, err
	}
	prepared, err := prepareInspectedBackup(ctx, cfg, request.PrepareRequest, source, plan, targets)
	if err != nil {
		return result, err
	}
	result = recovery.PublishFilesResult{Preparation: prepared, Role: request.Role, Remaining: remaining}
	if _, err := productfiles.NewDirectory(filepath.Dir(tree.Destination)); err != nil {
		return result, err
	}
	paths := cfg.EffectivePaths()
	owner := credentials.SelectionPolicyOwner{Product: "starport", Deployment: paths.DeploymentID, Instance: paths.InstanceID}
	result.Tree, err = source.PublishFileTree(ctx, tree, func(ctx context.Context, directory string) error {
		if request.Role == config.AcquisitionPolicyRole {
			settings := catalog.Settings{DeploymentID: paths.DeploymentID, InstanceID: paths.InstanceID}
			return settings.InspectCredentialPolicy(ctx, directory)
		}
		return credentials.InspectSelectionPolicyDirectory(ctx, directory, owner)
	})
	return result, err
}

func credentialPolicyRestoreSelection(cfg *config.Config, role string, plan []recovery.FileDisposition) (recovery.FileTreeRequest, []recovery.FileDisposition, error) {
	tree := recovery.FileTreeRequest{Destination: cfg.InferenceCredentialPolicyDirectory()}
	if role == config.AcquisitionPolicyRole {
		tree.Destination = cfg.CatalogCredentialPolicyDirectory()
	}
	remaining := make([]recovery.FileDisposition, 0, len(plan))
	for _, file := range plan {
		if file.Role != role {
			remaining = append(remaining, file)
			continue
		}
		if file.Action != "owner-recovery" || file.Destination == "" || tree.Destination == "" {
			return tree, nil, errors.New("credential policy publication requires the captured deployment and replica identity")
		}
		relative, err := filepath.Localize(file.Relative)
		if err != nil || filepath.Join(tree.Destination, relative) != file.Destination {
			return tree, nil, errors.New("credential policy destination differs from its canonical location")
		}
		tree.Files = append(tree.Files, recovery.FileTreeFile{ArtifactID: file.ArtifactID, Relative: file.Relative})
	}
	if len(tree.Files) == 0 {
		return tree, nil, errors.New("backup has no credential selection policy to publish")
	}
	return tree, remaining, nil
}
