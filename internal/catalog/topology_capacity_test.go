//go:build catalogcapacity

package catalog

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

// capacityOriginalBackup uses a real stopped runtime, native stores, and the canonical file census.
// Historical fixture inputs go through the producer's structural retention owner before backup.
func capacityOriginalBackup(t *testing.T, augment func(storage.KVStore, Settings, catalogs.Generation)) (*recovery.RestoreSource, Settings) {
	t.Helper()
	f := capacityOriginalBackupWith(t, nil, augment)
	return f.source, f.settings
}

type capacityBackup struct {
	source     *recovery.RestoreSource
	settings   Settings
	config     *config.Config
	kv         storage.KVStore
	db         *sqlstore.DB
	blobs      blob.Store
	encryption *credentials.EncryptionService
	directory  string
	manifest   recovery.BundleManifest
}

func capacityOriginalBackupWith(t *testing.T, beforeClose func(*Runtime, storage.KVStore, Settings, catalogs.Generation), augment func(storage.KVStore, Settings, catalogs.Generation)) *capacityBackup {
	t.Helper()
	root := filepath.Join(t.TempDir(), "source")
	_, err := productfiles.CreateDirectory(root)
	require.NoError(t, err)
	cfg, err := config.NewLoader().WithPaths(config.PathsForConfigDir(root)).WithEnvironment(map[string]string{"STARPORT_DEPLOYMENT_ID": "source", "STARPORT_INSTANCE_ID": "source-instance"}).WithEnvFiles().Load(t.Context())
	require.NoError(t, err)
	paths := cfg.EffectivePaths()
	kv, err := storage.OpenBadger(storage.BadgerConfig{Path: paths.BadgerDir, SyncWrites: true, MemTableSize: 64 << 20})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, kv.Close()) })
	settings := identityTestSettings(cfg.Catalog.StateDirectory, "", "")
	settings.DeploymentID, settings.InstanceID = paths.DeploymentID, paths.InstanceID
	settings.Values = map[string]string{catalogconfig.NetworkMode: "offline", catalogconfig.SchedulerIdentity: "capacity-source"}
	connected, err := OpenRuntime(t.Context(), kv, settings, nil)
	require.NoError(t, err)
	candidate, err := connected.CurrentCandidate(t.Context())
	require.NoError(t, err)
	require.NoError(t, connected.Accept(t.Context(), candidate))
	generation, err := connected.AcceptedGeneration(t.Context())
	require.NoError(t, err)
	if beforeClose != nil {
		beforeClose(connected, kv, settings, generation)
	}
	require.NoError(t, connected.Close(t.Context()))
	if augment != nil {
		augment(kv, settings, generation)
	}
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
	return capacityCaptureBackup(t, &capacityBackup{settings: settings, config: cfg, kv: kv, db: db, blobs: blobs, encryption: encryption}, capacityRecordTransfer(t, kv), boundary, filepath.Join(root, "backup"))
}

func capacityRecordTransfer(t *testing.T, kv storage.KVStore) storage.RecordTransfer {
	t.Helper()
	transfer, err := storage.OpenRecordTransfer(t.Context(), kv, "")
	require.NoError(t, err)
	return transfer
}

func capacityCaptureBackup(t *testing.T, f *capacityBackup, records storage.RecordSource, boundary recovery.Record, directory string) *capacityBackup {
	t.Helper()
	cfg := f.config
	inventory, err := cfg.CollectBackupInventory(t.Context(), "catalog-capacity-fixture", 100000)
	require.NoError(t, err)
	body, err := json.Marshal(inventory, json.Deterministic(true))
	require.NoError(t, err)
	inventoryPath := filepath.Join(cfg.EffectivePaths().ConfigDir, "captured-inventory.json")
	require.NoError(t, os.WriteFile(inventoryPath, body, 0600))
	files := []recovery.BundleFile{{ID: "inventory.json", Path: inventoryPath}}
	for _, file := range inventory.Files {
		files = append(files, recovery.BundleFile{ID: file.ArtifactID, Path: file.Source, ExpectedSHA256: file.ExpectedSHA256})
	}
	manifest, err := recovery.BackupBundle(t.Context(), directory, recovery.BundleSources{KV: records, SQL: f.db, Blobs: f.blobs, Encryption: f.encryption, Files: files}, recovery.BundleRequest{OperationID: "capacity-backup", Build: "catalog-capacity-fixture", Boundary: boundary, FencingEvidence: "all-fixture-writers-owned", KeyReference: "fixture/master"})
	require.NoError(t, err)
	digest, err := manifest.Digest()
	require.NoError(t, err)
	source, err := recovery.InspectRestoreSource(t.Context(), recovery.VerifyRequest{Directory: directory, ManifestSHA256: digest}, f.encryption)
	require.NoError(t, err)
	f.source, f.directory, f.manifest = source, directory, manifest
	return f
}

// capacityCompressedCapsule exercises a legal structural input with a large decoded identity.
// It is historical evidence, never the selected generation or a current authority identity.
func capacityCompressedCapsule(t *testing.T, id string, decodedIdentityBytes int) TopologyCapsule {
	t.Helper()
	capsule := topologySmallCapsule(t, id)
	body, err := json.Marshal(struct {
		Version       int                 `json:"version"`
		Baseline      catalogs.Generation `json:"baseline"`
		PublisherID   string              `json:"publisher_id"`
		Compatibility string              `json:"compatibility"`
	}{2, capsule.Generation, strings.Repeat("x", decodedIdentityBytes), strings.Repeat("c", 64)}, json.Deterministic(true))
	require.NoError(t, err)
	capsule.Recovery = capacityRecoveryRecord(t, capsule.Generation, body)
	return capsule
}

func capacityRecoveryRecord(t *testing.T, generation catalogs.Generation, body []byte) runtime.CatalogRecovery {
	t.Helper()
	var output bytes.Buffer
	writer, err := gzip.NewWriterLevel(&output, gzip.BestSpeed)
	require.NoError(t, err)
	_, err = writer.Write(body)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	manifest, err := json.Marshal(generation.Manifest, json.Deterministic(true))
	require.NoError(t, err)
	recovery := runtime.CatalogRecovery{ManifestChecksum: payloadDigest(manifest), Inputs: runtime.FleetRecovery{GenerationID: generation.Manifest.GenerationID, PayloadChecksum: generation.Manifest.Payload.Checksum, Data: output.Bytes(), Checksum: payloadDigest(output.Bytes())}}
	require.NoError(t, recovery.Validate(generation))
	return recovery
}

func capacityRetainOriginalCapsules(t *testing.T, kv storage.KVStore, settings Settings, selected catalogs.Generation, capsules []TopologyCapsule) {
	t.Helper()
	manifest := make([]runtime.CatalogRetentionEntry, len(capsules))
	for index, capsule := range capsules {
		ref, err := capsule.reference()
		require.NoError(t, err)
		manifest[index] = runtime.CatalogRetentionEntry{ManifestSHA256: ref.ManifestSHA256, InputsSHA256: ref.InputsSHA256, SourceOrigin: runtime.CatalogRetentionNoDescriptor}
	}
	accepted, err := NewGenerationStore(kv)
	require.NoError(t, err)
	previous := selected.Manifest.GenerationID
	for index, capsule := range capsules {
		request := runtime.CatalogRetentionRequest{Directory: settings.StateDirectory, Owner: settings.directoryOwner(), SchedulerIdentity: "capacity-source", OperationID: fmt.Sprintf("original-retain-%02d", index), TransferID: "original-capacity-inventory", Manifest: manifest, BatchStart: index, Inputs: []runtime.CatalogRetentionInput{{CatalogMaterializationInput: runtime.CatalogMaterializationInput{Generation: capsule.Generation, Recovery: capsule.Recovery}}}}
		receipt, err := runtime.RetainCatalogRecovery(t.Context(), request)
		require.NoError(t, err)
		require.Equal(t, "structural", receipt.Validation)
		require.NoError(t, accepted.Commit(t.Context(), capsule.Generation, previous))
		previous = capsule.Generation.Manifest.GenerationID
	}
	// Preserve actual selected runtime inputs after populating the owner-managed rollback records.
	require.NoError(t, accepted.Commit(t.Context(), selected, previous))
}

func TestRecoveryCatalogCapacityPacksDecodedProducerLimit(t *testing.T) {
	// Each capsule is individually legal. Their compressed sum fits one current consumer batch.
	// Their combined decoded size exceeds the producer's independent 256 MiB batch bound.
	source, settings := capacityOriginalBackup(t, func(kv storage.KVStore, settings Settings, selected catalogs.Generation) {
		capsules := []TopologyCapsule{capacityCompressedCapsule(t, "large-historical-a", (128<<20)+1024), capacityCompressedCapsule(t, "large-historical-b", (128<<20)+1024)}
		capacityRetainOriginalCapsules(t, kv, settings, selected, capsules)
	})
	compiled, err := CompileRecoveryTopology(t.Context(), source, topologyRequest(TopologyLocalRestore))
	require.NoError(t, err)
	record, err := compiled.Record()
	require.NoError(t, err)
	require.LessOrEqual(t, len(record), recoveryTopologyRecordMaxBytes)
	target, options, err := settings.RecoveryTopologyTarget()
	require.NoError(t, err)
	proof, err := compiled.RetainAndMaterialize(context.Background(), target, options...)
	require.NoError(t, err, "legal original capsules require batches that respect decoded and raw producer limits")
	require.Len(t, proof.receipts, 2, "both large capsules must not share a decoded batch")
	retained := 0
	for _, receipt := range proof.receipts {
		retained += len(receipt.Records)
	}
	require.Equal(t, 3, retained, "both historical capsules and the selected original must remain retained")
}
