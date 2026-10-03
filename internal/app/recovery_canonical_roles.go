package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/recovery"
)

const (
	canonicalRestoredState   = "restored-owner-state"
	canonicalCurrentTarget   = "current-target"
	canonicalVerifiedHistory = "verified-history"
	canonicalCaptureFiles    = "files"
)

var canonicalRecoveryRoles = []string{config.InferenceCredentialPolicyRole, config.AcquisitionPolicyRole, config.BaselineRole, config.RuntimeEvidenceRole}

func canonicalFileOwnerSelection(ctx context.Context, cfg *config.Config, source *recovery.RestoreSource, role string, tree recovery.FileTreeRequest, plan, remaining []recovery.FileDisposition) (recovery.FileTreeRequest, []recovery.FileDisposition, error) {
	switch role {
	case config.RuntimeEvidenceRole:
		return selectRuntimeRestoreFiles(ctx, cfg, source, tree, plan, remaining)
	case config.AcquisitionPolicyRole, config.InferenceCredentialPolicyRole:
		return selectCredentialRestoreFiles(ctx, cfg, role, source, tree, plan, remaining)
	case config.BaselineRole:
		return selectBaselineRestoreFiles(ctx, source, tree, plan, remaining)
	default:
		return tree, nil, recovery.ErrConflict
	}
}

func canonicalFileOwnerValidator(cfg *config.Config, role string) recovery.FileTreeValidator {
	return func(ctx context.Context, directory string) error {
		paths := cfg.EffectivePaths()
		switch role {
		case config.RuntimeEvidenceRole:
			return catalogSettings(cfg).InspectRetainedDirectory(ctx, directory)
		case config.BaselineRole:
			return catalog.InspectBaselineExports(ctx, directory)
		case config.AcquisitionPolicyRole:
			settings := catalog.Settings{DeploymentID: paths.DeploymentID, InstanceID: paths.InstanceID}
			return settings.InspectCredentialPolicy(ctx, directory)
		case config.InferenceCredentialPolicyRole:
			owner := credentials.SelectionPolicyOwner{Product: "starport", Deployment: paths.DeploymentID, Instance: paths.InstanceID}
			return credentials.InspectSelectionPolicyDirectory(ctx, directory, owner)
		default:
			return recovery.ErrConflict
		}
	}
}

type canonicalRoleSelection struct {
	role string
	tree recovery.FileTreeRequest
}

type canonicalRoleDisposition struct {
	Role        string `json:"role"`
	Capture     string `json:"capture"`
	Disposition string `json:"disposition"`
}

func canonicalRecoverySelectionMode(ctx context.Context, cfg *config.Config, source *recovery.RestoreSource, plan []recovery.FileDisposition, topology bool) ([]canonicalRoleSelection, []recovery.FileDisposition, []canonicalRoleDisposition, error) {
	dispositions := make(map[string]recovery.FileDisposition, len(plan))
	for _, file := range plan {
		dispositions[file.ArtifactID] = file
	}
	var selected []canonicalRoleSelection
	for _, role := range canonicalRecoveryRoles {
		if topology && (role == config.RuntimeEvidenceRole || slices.ContainsFunc(plan, func(file recovery.FileDisposition) bool { return file.Role == role && file.Action == "new-instance" })) {
			continue
		}
		if !slices.ContainsFunc(plan, func(file recovery.FileDisposition) bool { return file.Role == role }) {
			continue
		}
		tree, remaining, err := canonicalFileRestoreSelection(cfg, role, plan)
		if err != nil {
			return nil, nil, nil, err
		}
		tree, remaining, err = canonicalFileOwnerSelection(ctx, cfg, source, role, tree, plan, remaining)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, file := range remaining {
			if file.Role == role || role == config.BaselineRole && file.Role == config.BaselineRecoveryRole {
				dispositions[file.ArtifactID] = file
			}
		}
		for _, file := range tree.Files {
			disposition := dispositions[file.ArtifactID]
			disposition.Action = canonicalRestoredState
			disposition.Reason = "The role owner checks original publication and current canonical contents."
			dispositions[file.ArtifactID] = disposition
		}
		if len(tree.Files) != 0 {
			selected = append(selected, canonicalRoleSelection{role, tree})
		}
	}
	files := make([]recovery.FileDisposition, 0, len(plan))
	for _, original := range plan {
		file := dispositions[original.ArtifactID]
		file, err := canonicalRecoveryFileDisposition(file, topology)
		if err != nil {
			return nil, nil, nil, err
		}

		files = append(files, file)
	}
	inventory, err := readBackupInventory(ctx, source)
	if err != nil {
		return nil, nil, nil, err
	}
	roles := make([]canonicalRoleDisposition, 0, len(inventory.Roles))
	for _, role := range inventory.Roles {
		disposition := "inactive-history"
		if canonicalCurrentTargetRole(role.Entry.ID) || topology && topologyTargetRole(role.Entry.ID) {
			disposition = canonicalCurrentTarget
		}
		switch role.Capture {
		case canonicalCaptureFiles, "not-selected":
			for _, file := range files {
				if file.Role != role.Entry.ID {
					continue
				}
				if file.Action == canonicalCurrentTarget {
					disposition = canonicalCurrentTarget
				}
				if file.Action == canonicalRestoredState {
					disposition = canonicalRestoredState
				}
			}
		case "kv-snapshot", "sql-snapshot", "blob-snapshot":
			disposition = "component-owner"
		case "rebuild-under-source-policy":
			disposition = canonicalCurrentTarget
		default:
			return nil, nil, nil, recovery.ErrConflict
		}
		roles = append(roles, canonicalRoleDisposition{role.Entry.ID, role.Capture, disposition})
	}
	slices.SortFunc(roles, func(a, b canonicalRoleDisposition) int { return strings.Compare(a.Role, b.Role) })
	return selected, files, roles, nil
}

func canonicalRecoveryFileDisposition(file recovery.FileDisposition, topology bool) (recovery.FileDisposition, error) {
	if topology && (file.Role == config.RuntimeEvidenceRole || file.Action == "new-instance") {
		file.Action = canonicalVerifiedHistory
		file.Reason = "Captured instance evidence remains inactive. The current target owner and producer materialization establish current state."
	}
	if topology && topologyTargetRole(file.Role) {
		file.Action = canonicalCurrentTarget
		file.Reason = "Explicit current target workspace, setup, and cosmetic choices remain selected. Captured files stay inactive."
	}
	switch file.Action {
	case canonicalRestoredState, canonicalVerifiedHistory, canonicalCurrentTarget, "verified-staging":
	case "target-configuration", "operator-credential":
		file.Action = canonicalCurrentTarget
		file.Reason = "Current target inputs remain selected. Captured inputs remain inactive."
	case "retain-inactive":
		file.Action = canonicalVerifiedHistory
		if canonicalCurrentTargetRole(file.Role) {
			file.Action = canonicalCurrentTarget
			file.Reason = "Current target inputs remain selected. Captured inputs remain inactive."
		}
	default:
		return file, fmt.Errorf("canonical recovery requires a completed role owner disposition: %s/%s", file.Role, file.Action)
	}
	return file, nil
}

func canonicalCurrentTargetRole(role string) bool {
	return strings.HasPrefix(role, "dotenv-") || slices.Contains([]string{"configuration", "config-operation-journal", "source-file", "tls-certificate", "tls-key", "valkey-ca", "cache-ca", "local-token", "local-token-lock"}, role)
}
