package app

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
)

// PublishBackupFiles publishes one supported role at its configured canonical location.
// It verifies or resumes restricted preparation first. All writers must remain fenced.
// A publication error can leave a complete tree. Exact retries verify it without replacement.
func PublishBackupFiles(ctx context.Context, cfg *config.Config, request recovery.PublishFilesRequest) (result recovery.PublishFilesResult, err error) {
	if err := request.Validate(); err != nil {
		return result, err
	}
	if request.Role != config.InferenceCredentialPolicyRole && request.Role != config.AcquisitionPolicyRole && request.Role != config.BaselineRole && request.Role != config.RuntimeEvidenceRole {
		return result, errors.New("this file role requires a separate owner recovery procedure")
	}
	source, plan, targets, err := inspectBackupRestore(ctx, cfg, request.PrepareRequest)
	if err != nil {
		return result, err
	}
	tree, remaining, err := canonicalFileRestoreSelection(cfg, request.Role, plan)
	if err != nil {
		return result, err
	}
	tree, remaining, err = canonicalFileOwnerSelection(ctx, cfg, source, request.Role, tree, plan, remaining)
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
	result.Tree, err = source.PublishFileTree(ctx, tree, canonicalFileOwnerValidator(cfg, request.Role))
	return result, err
}

func canonicalFileRestoreSelection(cfg *config.Config, role string, plan []recovery.FileDisposition) (recovery.FileTreeRequest, []recovery.FileDisposition, error) {
	tree := recovery.FileTreeRequest{Destination: cfg.InferenceCredentialPolicyDirectory()}
	action := "owner-recovery"
	if role == config.BaselineRole {
		tree.Destination, action = cfg.EffectivePaths().BaselineDir, "verified-copy"
	}
	if role == config.RuntimeEvidenceRole {
		tree.Destination = cfg.EffectivePaths().RuntimeDir
	}
	if role == config.AcquisitionPolicyRole {
		tree.Destination = cfg.CatalogCredentialPolicyDirectory()
	}
	remaining := make([]recovery.FileDisposition, 0, len(plan))
	for _, file := range plan {
		if file.Role != role {
			remaining = append(remaining, file)
			continue
		}
		if file.Action != action || file.Destination == "" || tree.Destination == "" {
			return tree, nil, errors.New("file publication requires its selected canonical target and permitted owner action")
		}
		relative, err := filepath.Localize(file.Relative)
		if err != nil || filepath.Join(tree.Destination, relative) != file.Destination {
			return tree, nil, errors.New("file destination differs from its canonical location")
		}
		tree.Files = append(tree.Files, recovery.FileTreeFile{ArtifactID: file.ArtifactID, Relative: file.Relative})
	}
	if len(tree.Files) == 0 {
		return tree, nil, errors.New("backup has no files for the selected role")
	}
	return tree, remaining, nil
}
