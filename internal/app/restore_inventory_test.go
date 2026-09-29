package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/stretchr/testify/require"
)

// These bundles have valid outer hashes but invalid product inventory metadata.
func TestPrepareBackupRejectsInvalidInventoryBeforeTargetAccess(t *testing.T) {
	for _, mode := range []string{"deployment", "version", "duplicate-file", "missing-file", "unknown-role", "escape", "loaded-digest", "missing-role", "unknown-json"} {
		t.Run(mode, func(t *testing.T) {
			cfg, request := restoreApplicationFixture(t)
			name := filepath.Join(request.Directory, "files/inventory.json")
			body, err := os.ReadFile(name)
			require.NoError(t, err)
			var inventory config.BackupInventory
			require.NoError(t, json.Unmarshal(body, &inventory))
			switch mode {
			case "deployment":
				inventory.DeploymentID = "different-deployment"
			case "version":
				inventory.Version++
			case "duplicate-file":
				inventory.Files = append(inventory.Files, inventory.Files[0])
			case "missing-file":
				inventory.Files = nil
			case "unknown-role":
				inventory.Files[0].Role = "unknown-role"
			case "escape":
				inventory.Files[0].Relative = "../../escape"
			case "loaded-digest":
				inventory.Files[0].ExpectedSHA256 = "incorrect"
			case "missing-role":
				inventory.Roles = nil
			}
			body, err = json.Marshal(inventory)
			if mode == "unknown-json" {
				body = append([]byte(`{"unexpected":true,`), body[1:]...)
			}
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(name, body, 0o600))
			manifestPath := filepath.Join(request.Directory, "backup-manifest.json")
			manifestBody, err := os.ReadFile(manifestPath)
			require.NoError(t, err)
			var manifest recovery.BundleManifest
			require.NoError(t, json.Unmarshal(manifestBody, &manifest))
			digest := sha256.Sum256(body)
			for i := range manifest.Artifacts {
				if manifest.Artifacts[i].Path == "files/inventory.json" {
					manifest.Artifacts[i].Size = int64(len(body))
					manifest.Artifacts[i].SHA256 = hex.EncodeToString(digest[:])
				}
			}
			manifestBody, err = json.Marshal(manifest)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(manifestPath, manifestBody, 0o600))
			digest = sha256.Sum256(manifestBody)
			request.ManifestSHA256 = hex.EncodeToString(digest[:])
			_, err = PrepareBackup(t.Context(), cfg, request)
			require.Error(t, err)
			expected := map[string]string{
				"deployment": "inventory version or deployment", "version": "inventory version or deployment",
				"duplicate-file": "duplicate file identity", "missing-file": "every selected payload",
				"unknown-role": "no selected canonical role", "escape": "canonical location",
				"loaded-digest": "configuration digest differs", "missing-role": "omits canonical role", "unknown-json": "unexpected",
			}
			require.ErrorContains(t, err, expected[mode])
			require.NoDirExists(t, cfg.Storage.Badger.Path)
			require.NoFileExists(t, cfg.Storage.SQL.SQLite.Path)
			require.NoDirExists(t, request.FilesDirectory)
		})
	}
}
