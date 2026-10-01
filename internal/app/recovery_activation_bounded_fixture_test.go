package app

import (
	legacyjson "encoding/json"
	"encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func seedActivationGeneration(t *testing.T, cfg *config.Config, payload []byte) string {
	t.Helper()
	probe, err := filepath.Abs("testdata/probes/generation/activation_generation_probe_test.go")
	require.NoError(t, err)
	root, err := filepath.Abs("../..")
	require.NoError(t, err)
	temporary := t.TempDir()
	overlay, err := json.Marshal(map[string]any{"Replace": map[string]string{filepath.Join(root, "internal", "catalog", "settings_zero_test.go"): probe}})
	require.NoError(t, err)
	overlayPath := filepath.Join(temporary, "overlay.json")
	require.NoError(t, os.WriteFile(overlayPath, overlay, 0600))
	binary := filepath.Join(temporary, "generation-probe")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	command := exec.CommandContext(t.Context(), "go", "test", "-c", "-overlay", overlayPath, "-o", binary, "./internal/catalog")
	command.Dir = root
	command.Env = append(os.Environ(), "GOWORK=off")
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	body, err := json.Marshal(struct {
		Storage    storage.BadgerConfig
		Payload    []byte
		Settings   catalog.Settings
		ExportPath string
	}{cfg.RuntimeStorage().Badger, payload, catalogSettings(cfg), filepath.Join(temporary, "export.json")}, legacyjson.FormatDurationAsNano(true))
	require.NoError(t, err)
	fixture := filepath.Join(temporary, "fixture.json")
	require.NoError(t, os.WriteFile(fixture, body, 0600))
	command = exec.CommandContext(t.Context(), binary, "-test.run=^TestStarportActivationGenerationProbe$")
	command.Env = append(os.Environ(), "STARPORT_GENERATION_PROBE_FIXTURE="+fixture)
	output, err = command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	export := filepath.Join(temporary, "export.json")
	require.FileExists(t, export)
	return export
}

func boundedActivationSourceFixture(t *testing.T) (*config.Config, recovery.PrepareRequest) {
	t.Helper()
	return boundedActivationSourceFixtureWithStartup(t, false)
}

func boundedActivationSourceFixtureWithStartup(t *testing.T, normalStartup bool) (*config.Config, recovery.PrepareRequest) {
	t.Helper()
	return boundedActivationSourceFixtureWithActivity(t, normalStartup, nil, nil)
}

func boundedActivationSourceFixtureWithActivity(t *testing.T, normalStartup bool, beforeCapture func(*config.Config), afterCapture func(*config.Config, recovery.CaptureResult)) (*config.Config, recovery.PrepareRequest) {
	t.Helper()
	source, capture := backupApplicationFixture(t)
	paths := source.EffectivePaths()
	payload, err := catalogs.EncodeCatalogPayload(syntheticInferenceCatalog(t, "https://provider.invalid"))
	require.NoError(t, err)
	sourceFile := filepath.Join(paths.ConfigDir, "bounded-source.json")
	require.NoError(t, os.WriteFile(sourceFile, payload, 0600))
	values := map[string]string{"STARPORT_SECURITY_MASTER_KEY": source.Security.MasterKey, "STARPORT_DEPLOYMENT_ID": paths.DeploymentID, "STARPORT_INSTANCE_ID": paths.InstanceID, "STARPORT_CATALOG_SOURCE": "file", "STARPORT_CATALOG_SOURCE_URL": sourceFile, "STARPORT_CATALOG_SOURCE_STARTUP_POLICY": "require_source", "STARPORT_CATALOG_SOURCE_POLL_INTERVAL": "0s", "STARPORT_CATALOG_ACQUISITION_ENABLED": "false", "STARPORT_CATALOG_STARTUP_SPREAD": "0s"}
	source, err = config.NewLoader().WithPaths(paths).WithEnvFiles().WithEnvironment(values).Load(t.Context())
	require.NoError(t, err)
	export := seedActivationGeneration(t, source, payload)
	probeBinary := buildActivationProducerProbe(t, "runtime", "probes/materialization/activation_materialization_probe_test.go")
	probeCommand := exec.CommandContext(t.Context(), probeBinary, "-test.run=^TestStarportActivationMaterializationProbe$")
	probeCommand.Env = append(os.Environ(), "STARPORT_MATERIALIZATION_PROBE_FIXTURE="+export)
	probeOutput, probeErr := probeCommand.CombinedOutput()
	require.NoError(t, probeErr, "%s", probeOutput)
	if normalStartup {
		store, err := storage.Open(source.RuntimeStorage())
		require.NoError(t, err)
		connected, err := catalog.OpenRuntime(t.Context(), store, catalogSettings(source), nil)
		require.NoError(t, err)
		candidate, err := connected.CurrentCandidate(t.Context())
		require.NoError(t, err)
		require.NoError(t, connected.Accept(t.Context(), candidate))
		require.NoError(t, connected.Close(t.Context()))
		require.NoError(t, store.Close())
	}
	if beforeCapture != nil {
		beforeCapture(source)
	}
	_, err = CloseBackupBoundary(t.Context(), source)
	require.NoError(t, err)
	receipt, err := CaptureBackup(t.Context(), source, capture)
	require.NoError(t, err)
	if afterCapture != nil {
		afterCapture(source, receipt)
	}
	targetPath := filepath.Join(t.TempDir(), "target")
	_, err = productfiles.CreateDirectory(targetPath)
	require.NoError(t, err)
	target, err := config.NewLoader().WithPaths(config.PathsForConfigDir(targetPath)).WithEnvFiles().WithEnvironment(values).Load(t.Context())
	require.NoError(t, err)
	return target, recovery.PrepareRequest{VerifyRequest: recovery.VerifyRequest{Directory: receipt.Directory, ManifestSHA256: receipt.ManifestSHA256}, FilesDirectory: filepath.Join(targetPath, "prepared"), Operation: recovery.RestoreOperation{ID: "bounded-activation", FencingEvidence: "controlled-source-stopped"}}
}
