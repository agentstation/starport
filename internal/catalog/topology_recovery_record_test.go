package catalog

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type recoveryTopologyFixture struct {
	source    *recovery.RestoreSource
	inventory recovery.BackupInventory
	directory string
	witness   *recovery.Witness
	boundary  recovery.Record
}

func recoveryTopologyBackupFixture(t *testing.T) recoveryTopologyFixture {
	return recoveryTopologyBackupFixtureWith(t, nil, nil)
}

func recoveryTopologyBackupFixtureWith(t *testing.T, beforeCapture func(string), editInventory func(*recovery.BackupInventory)) recoveryTopologyFixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "source")
	_, err := productfiles.CreateDirectory(root)
	require.NoError(t, err)
	cfg, err := config.NewLoader().WithPaths(config.PathsForConfigDir(root)).WithEnvironment(map[string]string{"STARPORT_DEPLOYMENT_ID": "source", "STARPORT_INSTANCE_ID": "source-instance"}).WithEnvFiles().Load(t.Context())
	require.NoError(t, err)
	paths := cfg.EffectivePaths()
	kv, err := storage.OpenBadger(storage.BadgerConfig{Path: paths.BadgerDir, SyncWrites: true, NumVersions: 1, NumLevelZero: 5, MemTableSize: 64 << 20})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, kv.Close()) })
	settings := identityTestSettings(cfg.Catalog.StateDirectory, "", "")
	settings.DeploymentID, settings.InstanceID = paths.DeploymentID, paths.InstanceID
	settings.Values = map[string]string{catalogconfig.NetworkMode: "offline", catalogconfig.SchedulerIdentity: "backup-source"}
	connected, err := OpenRuntime(t.Context(), kv, settings, nil)
	require.NoError(t, err)
	candidate, err := connected.CurrentCandidate(t.Context())
	require.NoError(t, err)
	require.NoError(t, connected.Accept(t.Context(), candidate))
	require.NoError(t, connected.Close(t.Context()))
	_, err = productfiles.CreateDirectory(filepath.Dir(paths.SQLiteFile))
	require.NoError(t, err)
	db, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypeSQLite, SQLite: sqlstore.SQLiteConfig{Path: paths.SQLiteFile}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.Migrate(t.Context()))
	witness, err := recovery.New(db)
	require.NoError(t, err)
	boundary, err := witness.Initialize(t.Context(), paths.DeploymentID)
	require.NoError(t, err)
	blobs, err := blob.NewFilesystem(paths.FilesDir)
	require.NoError(t, err)
	encryption, err := credentials.NewEncryptionService([]byte(strings.Repeat("k", 32)))
	require.NoError(t, err)
	if beforeCapture != nil {
		beforeCapture(cfg.Catalog.StateDirectory)
	}
	inventory, err := cfg.CollectBackupInventory(t.Context(), "catalog-census-fixture", 10000)
	require.NoError(t, err)
	if editInventory != nil {
		editInventory(&inventory)
	}
	body, err := json.Marshal(inventory, json.Deterministic(true))
	require.NoError(t, err)
	inventoryPath := filepath.Join(root, "captured-inventory.json")
	require.NoError(t, os.WriteFile(inventoryPath, body, 0600))
	files := []recovery.BundleFile{{ID: "inventory.json", Path: inventoryPath}}
	for _, file := range inventory.Files {
		files = append(files, recovery.BundleFile{ID: file.ArtifactID, Path: file.Source, ExpectedSHA256: file.ExpectedSHA256})
	}
	transfer, err := storage.OpenRecordTransfer(t.Context(), kv, "")
	require.NoError(t, err)
	directory := filepath.Join(root, "backup")
	manifest, err := recovery.BackupBundle(t.Context(), directory, recovery.BundleSources{KV: transfer, SQL: db, Blobs: blobs, Encryption: encryption, Files: files}, recovery.BundleRequest{OperationID: "catalog-backup", Build: "catalog-census-fixture", Boundary: boundary, FencingEvidence: "all-fixture-writers-owned", KeyReference: "fixture/master"})
	require.NoError(t, err)
	digest, err := manifest.Digest()
	require.NoError(t, err)
	source, err := recovery.InspectRestoreSource(t.Context(), recovery.VerifyRequest{Directory: directory, ManifestSHA256: digest}, encryption)
	require.NoError(t, err)
	return recoveryTopologyFixture{source, inventory, directory, witness, boundary}
}
func TestRecoveryTopologyActualPersistentBackupSealsAndReopens(t *testing.T) {
	f := recoveryTopologyBackupFixtureWith(t, func(state string) {
		pending := filepath.Join(state, "catalog-runtime", "staging", "inactive.json")
		_, err := productfiles.CreateDirectory(filepath.Dir(pending))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(pending, []byte(`{"private":"fixture-secret-never-in-seal"}`), 0600))
	}, nil)
	direction, err := DeriveRecoveryTopologyDirection(t.Context(), f.source, true)
	require.NoError(t, err)
	require.Equal(t, TopologyLocalToFleet, direction)
	direction, err = DeriveRecoveryTopologyDirection(t.Context(), f.source, false)
	require.NoError(t, err)
	require.Equal(t, TopologyLocalRestore, direction)
	request := topologyRequest(TopologyLocalToFleet)
	compiled, err := CompileRecoveryTopology(t.Context(), f.source, request)
	require.NoError(t, err)
	require.NotEmpty(t, compiled.Digest())
	require.NotEqual(t, compiled.TopologyDigest(), compiled.Digest())
	record, err := compiled.Record()
	require.NoError(t, err)
	require.LessOrEqual(t, len(record), recoveryTopologyRecordMaxBytes)
	require.NotContains(t, string(record), `"inputs":`)
	require.NotContains(t, string(record), "source_descriptor")
	require.NotContains(t, string(record), "fixture-secret-never-in-seal")
	var sealed recoveryTopologyRecord
	require.NoError(t, json.Unmarshal(record, &sealed))
	require.Positive(t, sealed.Dispositions["inactive-runtime-history"])
	require.Positive(t, sealed.Dispositions["inactive-publication-history"])
	require.Equal(t, 1, sealed.Dispositions["original-reconstruction-inputs"])
	require.Equal(t, compiled.StageCount(), sealed.StageCount)
	require.Equal(t, compiled.recoveryStagesDigest(), sealed.StagesSHA256)
	detached, err := compiled.Record()
	require.NoError(t, err)
	detached[0] ^= 1
	require.NotEqual(t, record, detached)
	reopened, err := InspectRecoveryTopology(t.Context(), f.source, request, record, compiled.Digest())
	require.NoError(t, err)
	require.Equal(t, compiled.TopologyDigest(), reopened.TopologyDigest())
	require.Equal(t, compiled.Digest(), reopened.Digest())
	require.NoError(t, reopened.checkRetentionComparisons())
	require.Len(t, reopened.retentionComparisons, len(compiled.retentionComparisons))
	for index, comparison := range compiled.retentionComparisons {
		require.NotSame(t, comparison, reopened.retentionComparisons[index])
		require.Equal(t, comparison.Usage(), reopened.retentionComparisons[index].Usage())
	}
	require.Equal(t, compiled.stages, reopened.stages)
	require.Equal(t, compiled.selection, reopened.selection)
	current, err := f.witness.Current(t.Context(), f.boundary.DeploymentID)
	require.NoError(t, err)
	require.Equal(t, f.boundary, current, "passive compilation must not approve admission")
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		require.NotContains(t, fmt.Sprintf(format, reopened), f.directory)
	}
	t.Run("changed request", func(t *testing.T) {
		changed := request
		changed.AcceptedDecisionSHA256 = strings.Repeat("c", 64)
		_, err := InspectRecoveryTopology(t.Context(), f.source, changed, record, compiled.Digest())
		require.Error(t, err)
	})
	t.Run("wrong seal", func(t *testing.T) {
		_, err := InspectRecoveryTopology(t.Context(), f.source, request, record, strings.Repeat("a", 64))
		require.Error(t, err)
	})
	t.Run("changed retained file", func(t *testing.T) {
		var selected recovery.BackupFile
		for _, file := range f.inventory.Files {
			if strings.Contains(file.Relative, "generation-inputs/") {
				selected = file
				break
			}
		}
		require.NotEmpty(t, selected.ArtifactID)
		name := filepath.Join(f.directory, "files", filepath.FromSlash(selected.ArtifactID))
		original, err := os.ReadFile(name)
		require.NoError(t, err)
		changed := bytes.Clone(original)
		changed[0] ^= 1
		require.NoError(t, os.WriteFile(name, changed, 0600))
		_, err = InspectRecoveryTopology(t.Context(), f.source, request, record, compiled.Digest())
		require.Error(t, err)
		require.NoError(t, os.WriteFile(name, original, 0600))
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := InspectRecoveryTopology(ctx, f.source, request, record, compiled.Digest())
		require.ErrorIs(t, err, context.Canceled)
	})
}
func TestRecoveryTopologyRefusesLowLevelRecordAndMissingSource(t *testing.T) {
	var absent *CompiledTopology
	_, err := absent.Record()
	require.Error(t, err)
	require.Empty(t, absent.Digest())
	_, err = DeriveRecoveryTopologyDirection(t.Context(), nil, false)
	require.Error(t, err)
	_, err = (&CompiledTopology{}).Record()
	require.Error(t, err)
	_, err = CompileRecoveryTopology(t.Context(), nil, topologyRequest(TopologyLocalToFleet))
	require.Error(t, err)
	_, err = InspectRecoveryTopology(t.Context(), nil, topologyRequest(TopologyLocalToFleet), []byte("{}"), payloadDigest([]byte("{}")))
	require.Error(t, err)
}

func TestRecoveryTopologyRefusesIncompleteOriginalBackupCensus(t *testing.T) {
	f := recoveryTopologyBackupFixtureWith(t, nil, func(inventory *recovery.BackupInventory) {
		for index, file := range inventory.Files {
			if strings.Contains(file.Relative, "generation-inputs/") && strings.HasSuffix(file.Relative, ".json.gz") {
				inventory.Files = append(inventory.Files[:index:index], inventory.Files[index+1:]...)
				return
			}
		}
		t.Fatal("source owner did not retain its original inputs")
	})
	_, err := CompileRecoveryTopology(t.Context(), f.source, topologyRequest(TopologyLocalToFleet))
	require.ErrorContains(t, err, "omits an observed runtime file")
}

func TestRecoveryTopologyRefusesOldSourceWithoutReconstruction(t *testing.T) {
	f := recoveryTopologyBackupFixtureWith(t, func(state string) {
		entries, err := filepath.Glob(filepath.Join(state, "catalog-runtime", "generation-inputs", "*.json.gz"))
		require.NoError(t, err)
		require.NotEmpty(t, entries)
		for _, name := range entries {
			require.NoError(t, os.Remove(name))
		}
	}, nil)
	_, err := CompileRecoveryTopology(t.Context(), f.source, topologyRequest(TopologyLocalToFleet))
	require.ErrorContains(t, err, "recapture from its persistent source owner")
}

func TestRecoveryTopologySelectedGenerationRequiresOneOriginalReference(t *testing.T) {
	original := topologySmallCapsule(t, "same-generation")
	duplicate := original
	duplicate.SourceOrigin = "local-descriptor"
	duplicate.SourceDescriptor = []byte("different-original-descriptor")
	_, err := uniqueTopologyBackupReference([]TopologyCapsule{original, duplicate}, "same-generation")
	require.ErrorContains(t, err, "ambiguous")
	_, err = uniqueTopologyBackupReference([]TopologyCapsule{original}, "absent-generation")
	require.ErrorContains(t, err, "missing")
	ref, err := uniqueTopologyBackupReference([]TopologyCapsule{original}, "same-generation")
	require.NoError(t, err)
	expected, err := original.reference()
	require.NoError(t, err)
	require.Equal(t, expected, ref)
}
