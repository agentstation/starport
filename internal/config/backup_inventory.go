package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/agentstation/starmap/pkg/productpaths"
	"github.com/agentstation/starport/internal/recovery"
)

const backupCaptureFiles = "files"

// BackupFile identifies an original selected file in the recovery inventory.
type BackupFile = recovery.BackupFile

// BackupRole records the capture rule for an original canonical file role.
type BackupRole = recovery.BackupRole

// BackupInventory contains the recovery owner's original portable record.
type BackupInventory = recovery.BackupInventory

// CollectBackupInventory selects local files from the canonical product manifest.
// It refuses partial scans, unsupported file types, access conflicts, and unknown active roles.
// Writers must remain stopped and fenced until bundle capture finishes.
func (c *Config) CollectBackupInventory(ctx context.Context, build string, limit int) (BackupInventory, error) {
	if c == nil {
		return BackupInventory{}, errors.New("backup requires configuration")
	}
	report, err := c.FileManifest(build)
	if err != nil {
		return BackupInventory{}, err
	}
	result := BackupInventory{Version: 1, Build: build, DeploymentID: report.DeploymentID, InstanceID: report.InstanceID, External: report.External, Requirements: []string{
		"Recover environment settings and external configuration from the deployment owner.",
		"Recover the master encryption key and verify historical encrypted credentials.",
		"Restore access to inference and acquisition credentials through their configured sources.",
		"Restore cloud identity, secret-manager permissions, and upstream authority trust before activation.",
	}}
	selected := productpaths.FileManifest{SchemaVersion: report.SchemaVersion, Product: report.Product}
	indexes := make(map[string]int, len(report.Files))
	for _, entry := range report.Files {
		capture, err := backupRoleCapture(entry)
		if err != nil {
			return BackupInventory{}, err
		}
		indexes[entry.ID] = len(result.Roles)
		result.Roles = append(result.Roles, BackupRole{Entry: entry, Capture: capture})
		if capture == backupCaptureFiles {
			selected.Files = append(selected.Files, entry)
		}
	}
	inspected, err := productpaths.InspectManifest(ctx, selected, limit)
	if err != nil {
		return BackupInventory{}, err
	}
	if !inspected.Complete {
		return BackupInventory{}, errors.New("backup file inspection is incomplete")
	}
	loaded := make(map[string]string)
	for _, input := range c.fileInputs {
		if input.loaded {
			loaded[input.location.Path] = hex.EncodeToString(input.digest[:])
		}
	}
	for _, observation := range inspected.Observations {
		index, ok := indexes[observation.ID]
		if !ok {
			return BackupInventory{}, errors.New("backup observation has no canonical role")
		}
		role := &result.Roles[index]
		role.Observations = append(role.Observations, observation)
		if err := validateBackupObservation(role.Entry, observation, loaded[role.Entry.Location.Path] != ""); err != nil {
			return BackupInventory{}, err
		}
		if observation.State != "present" || observation.Kind != fileKindRegular {
			continue
		}
		relative := "."
		if role.Entry.Kind != fileKindRegular {
			relative, err = filepath.Rel(role.Entry.Location.Path, observation.Path)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return BackupInventory{}, errors.New("backup observation escaped its role")
			}
			relative = filepath.ToSlash(relative)
		}
		digest := sha256.Sum256([]byte(observation.ID + "\x00" + relative))
		result.Files = append(result.Files, BackupFile{ArtifactID: "inventory/" + hex.EncodeToString(digest[:]), Role: observation.ID, Relative: relative, Source: observation.Path, ExpectedSHA256: loaded[observation.Path]})
	}
	slices.SortFunc(result.Files, func(a, b BackupFile) int { return strings.Compare(a.ArtifactID, b.ArtifactID) })
	return result, nil
}

func backupRoleCapture(entry productpaths.FileEntry) (string, error) {
	if entry.Availability != "available" {
		return "not-selected", nil
	}
	switch entry.ID {
	case pathRoleBadger:
		return "kv-snapshot", nil
	case pathRoleSQLite, "sqlite-wal", "sqlite-shm", "sqlite-journal":
		return "sql-snapshot", nil
	case pathRoleFiles:
		return "blob-snapshot", nil
	case fileRoleSourceHTTP, fileRoleSourceCheckout:
		return "rebuild-under-source-policy", nil
	case pathRoleConfiguration, "setup-transaction", "setup-config-publications", "setup-storage-guard", "setup-database-stage",
		fileRoleLocalToken, "local-token-lock", fileRoleWelcome, pathRoleBaseline, fileRoleBaselineRecovery, fileRoleRuntimeEvidence,
		fileRoleAcquisitionPolicy, fileRoleInferencePolicy, fileRoleWorkspace, "workspace-receipt", "workspace-journal", "workspace-lock",
		"catalog-migration-lock", "workspace-preparing", "workspace-staging", "workspace-backup", fileRoleSourceFile, fileRoleTLSCertificate, fileRoleTLSKey, cacheCAFileRole, valkeyCAFileRole:
		return backupCaptureFiles, nil
	default:
		if strings.HasPrefix(entry.ID, "dotenv-") {
			return backupCaptureFiles, nil
		}
		return "", fmt.Errorf("backup has no capture rule for file role %s", entry.ID)
	}
}

func validateBackupObservation(entry productpaths.FileEntry, observation productpaths.FileObservation, configurationLoaded bool) error {
	if observation.State == "absent" {
		required := entry.ID == pathRoleConfiguration && configurationLoaded || strings.HasPrefix(entry.ID, "dotenv-") || slices.Contains([]string{fileRoleSourceFile, fileRoleTLSCertificate, fileRoleTLSKey, cacheCAFileRole, valkeyCAFileRole}, entry.ID)
		if required {
			return fmt.Errorf("backup requires the selected %s file", entry.ID)
		}
		return nil
	}
	if observation.State != "present" || observation.AccessStatus == "conflict" {
		return fmt.Errorf("backup cannot capture file role %s: %s", entry.ID, observation.AccessReason)
	}
	if observation.Kind != fileKindRegular && observation.Kind != "directory" {
		return fmt.Errorf("backup refuses unsupported content in file role %s", entry.ID)
	}
	if observation.Path == entry.Location.Path {
		if entry.Kind == fileKindRegular && observation.Kind != fileKindRegular || entry.Kind != fileKindRegular && observation.Kind != "directory" {
			return fmt.Errorf("backup file role %s has the wrong type", entry.ID)
		}
	}
	return nil
}
