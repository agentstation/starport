package app

import (
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestBackupApplicationRefusesInvalidCatalog(t *testing.T) {
	for _, mode := range []string{"accepted-pointer", "candidate-pointer", "generation", "history", "fleet-head"} {
		t.Run(mode, func(t *testing.T) {
			cfg, request := backupApplicationFixture(t)
			key, value := "catalog_generation:v1:current", []byte("missing-generation")
			switch mode {
			case "candidate-pointer":
				key = "catalog_candidate_generation:v1:current"
			case "generation":
				key, value = "catalog_generation:v1:generation:broken", []byte(`{"encoding":"chunked-json/1","size":-1}`)
			case "history":
				key, value = "catalog_generation:v1:index", []byte(`[{"generation_id":"missing"}]`)
			case "fleet-head":
				key, value = fmt.Sprintf("catalog:fleet:{%x}:v1:head", sha256.Sum256([]byte(cfg.EffectivePaths().DeploymentID))), []byte(`{}`)
			}
			kv, err := storage.Open(cfg.RuntimeStorage())
			require.NoError(t, err)
			require.NoError(t, kv.Set(t.Context(), key, value))
			require.NoError(t, kv.Close())
			_, err = CloseBackupBoundary(t.Context(), cfg)
			require.NoError(t, err)
			_, err = CaptureBackup(t.Context(), cfg, request)
			require.Error(t, err, "capture must not report a consistent backup with broken catalog references")
			// Keep the captured evidence, but refuse both verification and target preparation.
			body, err := os.ReadFile(filepath.Join(request.Destination, "backup-manifest.json"))
			require.NoError(t, err)
			var manifest recovery.BundleManifest
			require.NoError(t, json.Unmarshal(body, &manifest))
			digest, err := manifest.Digest()
			require.NoError(t, err)
			verify := recovery.VerifyRequest{Directory: request.Destination, ManifestSHA256: digest}
			_, err = VerifyBackup(t.Context(), cfg, verify)
			require.Error(t, err)
			target, err := config.NewLoader().WithPaths(config.PathsForConfigDir(filepath.Join(t.TempDir(), "target"))).WithEnvFiles().WithEnvironment(map[string]string{"STARPORT_SECURITY_MASTER_KEY": cfg.Security.MasterKey, "STARPORT_DEPLOYMENT_ID": cfg.EffectivePaths().DeploymentID}).Load(t.Context())
			require.NoError(t, err)
			prepare := recovery.PrepareRequest{VerifyRequest: verify, Operation: recovery.RestoreOperation{ID: "catalog-test", FencingEvidence: "test-stopped"}, FilesDirectory: filepath.Join(t.TempDir(), "prepared")}
			_, err = PrepareBackup(t.Context(), target, prepare)
			require.Error(t, err)
			require.NoDirExists(t, target.Storage.Badger.Path)
			require.NoFileExists(t, target.Storage.SQL.SQLite.Path)
			require.NoDirExists(t, prepare.FilesDirectory)

		})
	}
}
