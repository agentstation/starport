package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/agentstation/starmap/pkg/productpaths"
)

// RestoreFile describes the selected target and the procedure that owns publication.
// A destination is a proposed location. This report never authorizes a write.
type RestoreFile struct {
	ArtifactID  string
	Role        string
	Relative    string
	SHA256      string
	Destination string
	Action      string
	Reason      string
}

// PlanRestoreFiles validates captured inventory against the verified payload hashes.
// It derives destinations from current configuration, never from backup absolute paths.
// Call it before opening restore targets. It does not access files or the network.
func (c *Config) PlanRestoreFiles(inventory BackupInventory, payloads map[string]string) ([]RestoreFile, error) {
	if c == nil {
		return nil, errors.New("restore requires target configuration")
	}
	target, err := c.FileManifest("")
	if err != nil {
		return nil, err
	}
	if inventory.Version != 1 || inventory.DeploymentID != target.DeploymentID || inventory.InstanceID == "" {
		return nil, errors.New("restore inventory version or deployment identity differs")
	}
	targets := make(map[string]productpaths.FileEntry, len(target.Files))
	for _, entry := range target.Files {
		targets[entry.ID] = entry
	}
	roles, err := c.restoreInventoryRoles(inventory, targets)
	if err != nil {
		return nil, err
	}
	files := make([]RestoreFile, 0, len(inventory.Files))
	seen := make(map[string]bool, len(inventory.Files))
	for _, file := range inventory.Files {
		role, ok := roles[file.Role]
		if !ok || role.Capture != backupCaptureFiles {
			return nil, errors.New("restore file has no selected canonical role")
		}
		if err := validateRestoreRelative(file.Relative, role.Entry.Kind); err != nil {
			return nil, err
		}
		digest := sha256.Sum256([]byte(file.Role + "\x00" + file.Relative))
		if file.ArtifactID != "inventory/"+hex.EncodeToString(digest[:]) || seen[file.ArtifactID] {
			return nil, errors.New("restore inventory contains an invalid or duplicate file identity")
		}
		seen[file.ArtifactID] = true
		actual, exists := payloads[file.ArtifactID]
		decoded, err := hex.DecodeString(actual)
		if !exists || err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != actual {
			return nil, errors.New("restore inventory file has no verified payload")
		}
		if file.ExpectedSHA256 != "" && file.ExpectedSHA256 != actual {
			return nil, errors.New("restore loaded configuration digest differs from its payload")
		}
		destination, action, reason, err := restoreFileDestination(file, targets, inventory.InstanceID == target.InstanceID)
		if err != nil {
			return nil, err
		}
		files = append(files, RestoreFile{ArtifactID: file.ArtifactID, Role: file.Role, Relative: file.Relative, SHA256: actual, Destination: destination, Action: action, Reason: reason})
	}
	if len(seen) != len(payloads) {
		return nil, errors.New("restore inventory does not account for every selected payload")
	}
	if err := validateRestoreDestinations(files); err != nil {
		return nil, err
	}
	slices.SortFunc(files, func(a, b RestoreFile) int { return strings.Compare(a.ArtifactID, b.ArtifactID) })
	return files, nil
}

func validateRestoreRole(role BackupRole, targets map[string]productpaths.FileEntry) error {
	entry := role.Entry
	expected, known := targets[entry.ID]
	if index, dotenv := strings.CutPrefix(entry.ID, "dotenv-"); dotenv {
		n, err := strconv.Atoi(index)
		if err != nil || n < 0 || strconv.Itoa(n) != index {
			return errors.New("restore inventory contains an invalid environment-file role")
		}
	}
	if entry.ID == fileRoleSourceFile || strings.HasPrefix(entry.ID, "dotenv-") {
		expected.Kind, known = fileKindRegular, true
	}
	if !known || entry.Kind != expected.Kind {
		return fmt.Errorf("restore inventory has an unknown role or file kind: %s", entry.ID)
	}
	if !slices.Contains([]string{fileAvailable, fileDisabled, filePlanned}, entry.Availability) {
		return errors.New("restore inventory has an invalid role selection")
	}
	capture, err := backupRoleCapture(entry)
	if err != nil {
		return err
	}
	if role.Capture != capture {
		return errors.New("restore inventory capture method differs from its role")
	}
	return nil
}

func validateRestoreRelative(relative, kind string) error {
	if kind == fileKindRegular {
		if relative != "." {
			return errors.New("restore regular file must use its canonical location")
		}
		return nil
	}
	if relative == "." || !fs.ValidPath(relative) || strings.ContainsAny(relative, "\\:") || strings.ContainsFunc(relative, unicode.IsControl) {
		return errors.New("restore file has an unsafe relative name")
	}
	for _, part := range strings.Split(relative, "/") {
		stem := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || slices.Contains([]string{"CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9"}, stem) {
			return errors.New("restore file name is not portable")
		}
	}
	_, err := filepath.Localize(relative)
	return err
}

func restoreFileDestination(file BackupFile, targets map[string]productpaths.FileEntry, sameInstance bool) (destination, action, reason string, err error) {
	action, reason = "owner-recovery", "The owning component must verify retained state before publication."
	switch file.Role {
	case pathRoleConfiguration, fileRoleSourceFile, fileRoleTLSCertificate, fileRoleTLSKey, cacheCAFileRole, valkeyCAFileRole:
		action, reason = "target-configuration", "Keep current target settings and trust. Reconcile the captured input explicitly."
	case fileRoleLocalToken, "local-token-lock":
		action, reason = "operator-credential", "Reconcile administrator access before creating or restoring a local token."
	case pathRoleBaseline, fileRoleWelcome:
		action, reason = "verified-copy", "Publish only after content verification and a target conflict check."
	case fileRoleRuntimeEvidence, fileRoleAcquisitionPolicy, fileRoleInferencePolicy:
		if !sameInstance {
			action, reason = "new-instance", "Create a distinct replica identity. Recover required evidence through its owner."
		}
	}
	if strings.HasPrefix(file.Role, "dotenv-") {
		return "", "target-configuration", "Select target environment files explicitly. Source indexes do not identify target files.", nil
	}
	target, selected := targets[file.Role]
	if !selected || target.Location.Path == "" || target.Availability != fileAvailable && file.Role != pathRoleConfiguration {
		return "", "retain-inactive", "The target does not select this role. Keep its captured files in inactive recovery storage.", nil
	}
	if !filepath.IsAbs(target.Location.Path) || filepath.Clean(target.Location.Path) != target.Location.Path {
		return "", "", "", errors.New("restore requires clean absolute canonical target paths")
	}
	// Pattern roles can include an old workspace basename or path-bound journals.
	// Their owner must choose the replacement names, including on a different OS.
	if target.Kind == fileKindPatterns {
		return "", action, reason, nil
	}
	destination = target.Location.Path
	if target.Kind != fileKindRegular {
		relative, localErr := filepath.Localize(file.Relative)
		if localErr != nil {
			return "", "", "", localErr
		}
		destination = filepath.Join(destination, relative)
	}
	return destination, action, reason, nil
}

// Reject lexical collisions before publication. Native publication must also check file identities.
func validateRestoreDestinations(files []RestoreFile) error {
	destinations := make(map[string]bool, len(files))
	for _, file := range files {
		if file.Destination == "" {
			continue
		}
		name := strings.ToLower(filepath.Clean(file.Destination))
		if destinations[name] {
			return errors.New("restore canonical file destinations conflict")
		}
		destinations[name] = true
	}
	for name := range destinations {
		for parent := filepath.Dir(name); parent != name; parent = filepath.Dir(parent) {
			if destinations[parent] {
				return errors.New("restore canonical file destinations conflict")
			}
			if filepath.Dir(parent) == parent {
				break
			}
		}
	}
	return nil
}

func (c *Config) restoreInventoryRoles(inventory BackupInventory, targets map[string]productpaths.FileEntry) (map[string]BackupRole, error) {
	schema := maps.Clone(targets)
	workspace, err := productpaths.WorkspaceFiles(productpaths.Path{Path: filepath.Join(c.EffectivePaths().ConfigDir, "restore-workspace-schema")})
	if err != nil {
		return nil, err
	}
	for _, entry := range workspace {
		schema[entry.ID] = entry
	}
	roles := make(map[string]BackupRole, len(inventory.Roles))
	for _, role := range inventory.Roles {
		if _, duplicate := roles[role.Entry.ID]; duplicate {
			return nil, errors.New("restore inventory contains duplicate roles")
		}
		if err := validateRestoreRole(role, schema); err != nil {
			return nil, err
		}
		roles[role.Entry.ID] = role
	}
	// Version one always records static roles, including disabled selections.
	for id := range schema {
		if id == fileRoleSourceFile || strings.HasPrefix(id, "dotenv-") {
			continue
		}
		if id != fileRoleWorkspace && (strings.HasPrefix(id, "workspace-") || id == "catalog-migration-lock") && roles[fileRoleWorkspace].Entry.Availability != fileAvailable {
			continue
		}
		if _, exists := roles[id]; !exists {
			return nil, fmt.Errorf("restore inventory omits canonical role %s", id)
		}
	}
	return roles, nil
}
