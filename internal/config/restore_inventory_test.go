package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starmap/pkg/productpaths"
	"github.com/stretchr/testify/require"
)

func restoreInventoryFixture(t *testing.T) (*Config, BackupInventory, map[string]string) {
	t.Helper()
	source := backupInventoryConfig(t, nil)
	paths := source.EffectivePaths()
	writeInventoryFixture(t, paths.LocalTokenFile, "private")
	writeInventoryFixture(t, filepath.Join(paths.RuntimeDir, "owner.json"), "owner")
	writeInventoryFixture(t, filepath.Join(paths.BaselineDir, "generation", "catalog.json"), "baseline")
	writeInventoryFixture(t, filepath.Join(source.CatalogCredentialPolicyDirectory(), "policy.json"), "policy")
	source.Catalog.WorkspacePath = filepath.Join(paths.ConfigDir, "old-workspace")
	writeInventoryFixture(t, filepath.Join(source.Catalog.WorkspacePath, "模型.yaml"), "workspace")
	writeInventoryFixture(t, filepath.Join(paths.ConfigDir, ".old-workspace.backup-one", "models.yaml"), "backup")
	inventory, err := source.CollectBackupInventory(t.Context(), "test", 1000)
	require.NoError(t, err)
	hashes := make(map[string]string)
	for _, file := range inventory.Files {
		body, err := os.ReadFile(file.Source)
		require.NoError(t, err)
		digest := sha256.Sum256(body)
		hashes[file.ArtifactID] = hex.EncodeToString(digest[:])
	}
	target := backupInventoryConfig(t, nil)
	target.Catalog.WorkspacePath = filepath.Join(target.EffectivePaths().ConfigDir, "new-workspace")
	return target, inventory, hashes
}

func TestRestoreInventoryUsesTargetConfigurationAndOwnerProcedures(t *testing.T) {
	target, inventory, hashes := restoreInventoryFixture(t)
	// Source absolute paths can belong to another host and platform.
	for i := range inventory.Roles {
		inventory.Roles[i].Entry.Location.Path = `C:\untrusted\source`
	}
	plan, err := target.PlanRestoreFiles(inventory, hashes)
	require.NoError(t, err)
	require.Len(t, plan, len(inventory.Files))
	byRole := make(map[string]RestoreFile)
	for _, file := range plan {
		byRole[file.Role] = file
		require.NotContains(t, file.Destination, "untrusted")
	}
	require.Equal(t, target.EffectivePaths().LocalTokenFile, byRole["local-token"].Destination)
	require.Equal(t, "operator-credential", byRole["local-token"].Action)
	require.Equal(t, "owner-recovery", byRole["runtime-evidence"].Action)
	require.Equal(t, "verified-copy", byRole["baseline"].Action)
	require.Equal(t, filepath.Join(target.Catalog.WorkspacePath, "模型.yaml"), byRole["workspace"].Destination)
	require.Equal(t, "owner-recovery", byRole["workspace-backup"].Action)
	require.Empty(t, byRole["workspace-backup"].Destination, "the owner must map renamed workspace recovery files")
	require.NoDirExists(t, target.EffectivePaths().DataDir)
	require.NoDirExists(t, target.EffectivePaths().StateDir)
}

func TestRestoreInventoryDoesNotCloneReplicaIdentity(t *testing.T) {
	target, inventory, hashes := restoreInventoryFixture(t)
	inventory.InstanceID = "retired-replica"
	plan, err := target.PlanRestoreFiles(inventory, hashes)
	require.NoError(t, err)
	found := 0
	for _, file := range plan {
		if file.Role == "runtime-evidence" || file.Role == "credential-policy" {
			require.Equal(t, "new-instance", file.Action)
			found++
		}
	}
	require.Equal(t, 2, found)
}

func TestRestoreInventoryKeepsUnselectedAndConfigurationInputsInactive(t *testing.T) {
	target, inventory, hashes := restoreInventoryFixture(t)
	target.Catalog.WorkspacePath = ""
	for _, role := range []string{"configuration", "dotenv-7", "config-operation-journal"} {
		found := false
		for i := range inventory.Roles {
			if inventory.Roles[i].Entry.ID == role {
				inventory.Roles[i].Entry.Availability = fileAvailable
				inventory.Roles[i].Capture = backupCaptureFiles
				found = true
			}
		}
		if !found {
			inventory.Roles = append(inventory.Roles, BackupRole{Entry: productpaths.FileEntry{ID: role, Kind: fileKindRegular, Availability: fileAvailable}, Capture: backupCaptureFiles})
		}
		digest := sha256.Sum256([]byte(role + "\x00."))
		id := "inventory/" + hex.EncodeToString(digest[:])
		inventory.Files = append(inventory.Files, BackupFile{Role: role, Relative: ".", ArtifactID: id})
		hashes[id] = strings.Repeat("a", 64)
	}
	plan, err := target.PlanRestoreFiles(inventory, hashes)
	require.NoError(t, err)
	for _, file := range plan {
		switch file.Role {
		case "configuration":
			require.Equal(t, "target-configuration", file.Action)
			require.Equal(t, target.EffectivePaths().ConfigFile, file.Destination)
		case "dotenv-7":
			require.Equal(t, "target-configuration", file.Action)
			require.Empty(t, file.Destination)
		case "config-operation-journal":
			// The target selects no configuration file, so it has no journal.
			require.Equal(t, "retain-inactive", file.Action)
			require.Empty(t, file.Destination)
		case "workspace":
			require.Equal(t, "retain-inactive", file.Action)
			require.Empty(t, file.Destination)
		}
	}
	require.NoFileExists(t, target.EffectivePaths().ConfigFile)
}

func TestRestoreInventoryKeepsCapturedOperationJournalWithTargetConfiguration(t *testing.T) {
	initial := backupInventoryConfig(t, nil)
	configFile := initial.EffectivePaths().ConfigFile
	writeInventoryFixture(t, configFile, "STARPORT_LOG_LEVEL=info\n")
	journal := filepath.Join(filepath.Dir(configFile), localJournalName)
	writeInventoryFixture(t, journal, `{"operations":[]}`)
	source, err := NewLoader().WithPaths(initial.EffectivePaths()).WithEnvironment(map[string]string{"STARPORT_CONFIG_FILE": configFile}).Load(t.Context())
	require.NoError(t, err)
	inventory, err := source.CollectBackupInventory(t.Context(), "test", 1000)
	require.NoError(t, err)
	hashes := make(map[string]string)
	for _, file := range inventory.Files {
		body, err := os.ReadFile(file.Source)
		require.NoError(t, err)
		digest := sha256.Sum256(body)
		hashes[file.ArtifactID] = hex.EncodeToString(digest[:])
	}
	targetFile := filepath.Join(t.TempDir(), "target", "starport.env")
	writeInventoryFixture(t, targetFile, "STARPORT_LOG_LEVEL=warn\n")
	target, err := NewLoader().WithPaths(PathsForConfigDir(filepath.Dir(targetFile))).WithEnvironment(map[string]string{"STARPORT_CONFIG_FILE": targetFile}).Load(t.Context())
	require.NoError(t, err)
	plan, err := target.PlanRestoreFiles(inventory, hashes)
	require.NoError(t, err)
	require.Len(t, plan, 2)
	for _, file := range plan {
		// The journal binds receipts to the file checksum, so it follows
		// the configuration disposition instead of a plain copy.
		require.Equal(t, "target-configuration", file.Action)
	}
	byRole := make(map[string]RestoreFile)
	for _, file := range plan {
		byRole[file.Role] = file
	}
	require.Equal(t, targetFile, byRole["configuration"].Destination)
	require.Equal(t, filepath.Join(filepath.Dir(targetFile), localJournalName), byRole["config-operation-journal"].Destination)
	require.NoFileExists(t, filepath.Join(filepath.Dir(targetFile), localJournalName))
}

func TestRestoreInventoryRejectsUnsafeNamesAndMismatchedPayloads(t *testing.T) {
	for _, mode := range []string{"duplicate-role", "unknown-disabled-role", "bad-kind", "bad-capture", "bad-selection", "bad-artifact", "extra-payload", "missing-payload", "bad-hash", "traversal", "windows-path", "reserved-name", "trailing-dot", "control", "bad-dotenv"} {
		t.Run(mode, func(t *testing.T) {
			target, inventory, hashes := restoreInventoryFixture(t)
			switch mode {
			case "duplicate-role":
				inventory.Roles = append(inventory.Roles, inventory.Roles[0])
			case "unknown-disabled-role":
				inventory.Roles = append(inventory.Roles, BackupRole{Entry: productpaths.FileEntry{ID: "future-role", Availability: fileDisabled}, Capture: "not-selected"})
			case "bad-kind":
				inventory.Roles[0].Entry.Kind = fileKindTree
			case "bad-capture":
				inventory.Roles[0].Capture = "files"
			case "bad-selection":
				inventory.Roles[0].Entry.Availability = "unexpected"
			case "bad-artifact":
				inventory.Files[0].ArtifactID = "inventory/invalid"
			case "extra-payload":
				hashes["inventory/extra"] = strings.Repeat("a", 64)
			case "missing-payload":
				delete(hashes, inventory.Files[0].ArtifactID)
			case "bad-hash":
				hashes[inventory.Files[0].ArtifactID] = "invalid"
			case "bad-dotenv":
				inventory.Roles = append(inventory.Roles, BackupRole{Entry: productpaths.FileEntry{ID: "dotenv-../escape", Kind: fileKindRegular, Availability: fileAvailable}, Capture: "files"})
			default:
				names := map[string]string{"traversal": "../escape", "windows-path": `dir\escape`, "reserved-name": "CON.txt", "trailing-dot": "a.", "control": "a\nb"}
				for i := range inventory.Files {
					if inventory.Files[i].Role == "workspace" {
						inventory.Files[i].Relative = names[mode]
						digest := sha256.Sum256([]byte("workspace\x00" + names[mode]))
						old := inventory.Files[i].ArtifactID
						inventory.Files[i].ArtifactID = "inventory/" + hex.EncodeToString(digest[:])
						hashes[inventory.Files[i].ArtifactID] = hashes[old]
						delete(hashes, old)
					}
				}
			}
			_, err := target.PlanRestoreFiles(inventory, hashes)
			require.Error(t, err)
			require.NoDirExists(t, target.EffectivePaths().DataDir)
		})
	}
}

func TestRestoreFileDestinationsRejectCollisions(t *testing.T) {
	for _, names := range [][]string{{"a", "a"}, {"A", "a"}, {"a", "a-b", "a/child"}} {
		var files []RestoreFile
		for _, name := range names {
			files = append(files, RestoreFile{Destination: filepath.Join(string(filepath.Separator), "target", name)})
		}
		require.Error(t, validateRestoreDestinations(files))
	}
	require.NoError(t, validateRestoreDestinations([]RestoreFile{{Destination: filepath.Join(string(filepath.Separator), "a")}, {Destination: filepath.Join(string(filepath.Separator), "b")}, {}}))
}
