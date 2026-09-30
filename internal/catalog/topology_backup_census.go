package catalog

import (
	"context"
	"encoding/json/v2"
	"errors"
	"path"
	"slices"
	"strings"

	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
)

const topologyRuntimeEvidenceRole = "runtime-evidence"

type topologyBackupFile struct {
	File        recovery.BackupFile     `json:"file"`
	Artifact    recovery.BundleArtifact `json:"artifact"`
	Disposition string                  `json:"disposition"`
}
type topologyBackupCensus struct {
	source       *recovery.RestoreSource
	inventorySHA string
	files        []topologyBackupFile
	runtime      map[string]recovery.BackupFile
}

func readTopologyBackupCensus(ctx context.Context, source *recovery.RestoreSource) (*topologyBackupCensus, error) {
	raw, err := source.SelectedFile(ctx, "inventory.json", 16<<20)
	if err != nil {
		return nil, err
	}
	var inventory recovery.BackupInventory
	if err := json.Unmarshal(raw, &inventory, json.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if inventory.Version != 1 || inventory.DeploymentID != source.CapturedBoundary().DeploymentID {
		return nil, recovery.ErrConflict
	}
	roles := map[string]recovery.BackupRole{}
	for _, role := range inventory.Roles {
		if role.Entry.ID == "" {
			return nil, recovery.ErrConflict
		}
		if _, exists := roles[role.Entry.ID]; exists {
			return nil, recovery.ErrConflict
		}
		roles[role.Entry.ID] = role
	}
	role, exists := roles[topologyRuntimeEvidenceRole]
	if !exists || (role.Capture != "files" && role.Capture != "not-selected") {
		return nil, errors.New("catalog backup requires its original runtime file role")
	}
	census := &topologyBackupCensus{source: source, inventorySHA: payloadDigest(raw), runtime: map[string]recovery.BackupFile{}}
	artifacts := source.SelectedFileArtifacts()
	delete(artifacts, "inventory.json")
	seen := map[string]bool{}
	var total int64
	for _, file := range inventory.Files {
		artifact, err := bindTopologyBackupFile(file, artifacts, roles, seen)
		if err != nil {
			return nil, err
		}
		retained := topologyBackupFile{File: file, Artifact: artifact, Disposition: "inactive-operator-file"}
		if file.Role == topologyRuntimeEvidenceRole {
			if _, found := census.runtime[file.Relative]; found {
				return nil, recovery.ErrConflict
			}
			if artifact.Size < 0 || int64(len(file.Relative)) > fleetRetentionMaxBytes-total || artifact.Size > fleetRetentionMaxBytes-total-int64(len(file.Relative)) {
				return nil, storage.ErrValueTooLarge
			}
			total += int64(len(file.Relative)) + artifact.Size
			census.runtime[file.Relative] = file
			retained.Disposition = "inactive-runtime-history"
			if _, err := source.SelectedPayload(ctx, file.ArtifactID, topologyBackupPayloadLimit(file.Relative)); err != nil {
				return nil, err
			}
		}
		census.files = append(census.files, retained)
	}
	if len(seen) != len(artifacts) {
		return nil, errors.New("catalog backup inventory omits selected files")
	}
	if err := checkTopologyRuntimeObservations(role, census.runtime); err != nil {
		return nil, err
	}
	slices.SortFunc(census.files, func(a, b topologyBackupFile) int { return strings.Compare(a.File.ArtifactID, b.File.ArtifactID) })
	return census, nil
}

func checkTopologyRuntimeObservations(role recovery.BackupRole, files map[string]recovery.BackupFile) error {
	observed := map[string]bool{}
	root := strings.ReplaceAll(role.Entry.Location.Path, "\\", "/")
	for _, entry := range role.Observations {
		if entry.State == "absent" {
			continue
		}
		if entry.State != "present" || entry.AccessStatus == "conflict" {
			return recovery.ErrConflict
		}
		if entry.Kind == "directory" {
			continue
		}
		if entry.Kind != "file" {
			return recovery.ErrConflict
		}
		original := strings.ReplaceAll(entry.Path, "\\", "/")
		relative, found := strings.CutPrefix(original, strings.TrimSuffix(root, "/")+"/")
		if !found || path.Clean(relative) != relative || observed[relative] {
			return recovery.ErrConflict
		}
		if _, found := files[relative]; !found {
			return errors.New("catalog backup omits an observed runtime file")
		}
		observed[relative] = true
	}
	if len(observed) != len(files) {
		return errors.New("catalog backup runtime observations omit captured files")
	}
	return nil
}
func (c *topologyBackupCensus) digest() (string, error) {
	raw, err := json.Marshal(c.files, json.Deterministic(true))
	if err != nil {
		return "", err
	}
	return payloadDigest(raw), nil
}
func (c *topologyBackupCensus) dispositions() map[string]int {
	result := map[string]int{}
	for _, file := range c.files {
		result[file.Disposition]++
	}
	return result
}
func (c *topologyBackupCensus) disposition(relative, value string) {
	for index := range c.files {
		if c.files[index].File.Role == topologyRuntimeEvidenceRole && c.files[index].File.Relative == relative {
			c.files[index].Disposition = value
			return
		}
	}
}

func topologyBackupPayloadLimit(relative string) int64 {
	if strings.HasPrefix(relative, "catalog-runtime/retained-catalog/") && strings.HasSuffix(relative, ".json.gz") {
		return runtime.MaxCatalogRetentionRecordBytes
	}
	return runtime.MaxFleetRecoveryBytes
}

func bindTopologyBackupFile(file recovery.BackupFile, artifacts map[string]recovery.BundleArtifact, roles map[string]recovery.BackupRole, seen map[string]bool) (recovery.BundleArtifact, error) {
	artifact, found := artifacts[file.ArtifactID]
	selected, roleFound := roles[file.Role]
	if !found || !roleFound || selected.Capture != "files" || seen[file.ArtifactID] || file.Relative == "" || path.Clean(file.Relative) != file.Relative || strings.HasPrefix(file.Relative, "/") || file.Relative == ".." || strings.HasPrefix(file.Relative, "../") || strings.Contains(file.Relative, `\`) {
		return recovery.BundleArtifact{}, recovery.ErrConflict
	}
	if file.ExpectedSHA256 != "" && file.ExpectedSHA256 != artifact.SHA256 {
		return recovery.BundleArtifact{}, recovery.ErrConflict
	}
	seen[file.ArtifactID] = true
	return artifact, nil
}
