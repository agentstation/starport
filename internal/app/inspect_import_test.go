package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func importedInspectionFixture(t *testing.T) (*config.Config, recovery.InspectImportRequest, recovery.PreparedImportIdentity) {
	t.Helper()
	cfg, prepare := restoreApplicationFixture(t)
	prepared, err := PrepareBackup(t.Context(), cfg, prepare)
	require.NoError(t, err)
	encryption, err := backupEncryption(cfg)
	require.NoError(t, err)
	source, err := recovery.InspectRestoreSource(t.Context(), prepare.VerifyRequest, encryption, catalog.InspectCapturedCatalog)
	require.NoError(t, err)
	identity, err := source.ImportIdentity(prepare.Operation)
	require.NoError(t, err)
	request := recovery.InspectImportRequest{VerifyRequest: prepare.VerifyRequest, Operation: prepare.Operation, ExpectedBoundary: prepared.Prepared.Boundary, Destination: filepath.Join(filepath.Dir(prepare.FilesDirectory), "inspection")}
	return cfg, request, identity
}

func assertInspectionStillClosed(t *testing.T, cfg *config.Config) {
	t.Helper()
	_, err := storage.Open(cfg.RuntimeStorage())
	require.ErrorIs(t, err, storage.ErrImportRestricted)
	db, err := openBackupSQL(cfg)
	require.NoError(t, err)
	require.ErrorIs(t, db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
	require.NoError(t, db.Close())
	if cfg.Files.SelectedBackend() == config.BlobBackendFilesystem {
		_, err = blob.NewFilesystem(cfg.Files.Path)
		require.ErrorIs(t, err, blob.ErrImportRestricted)
	}
	require.NoFileExists(t, cfg.EffectivePaths().LocalTokenFile)
}

func TestInspectImportedBackupRetainsCompletePrivateReceipt(t *testing.T) {
	cfg, request, _ := importedInspectionFixture(t)
	result, err := InspectImportedBackup(t.Context(), cfg, request)
	require.NoError(t, err)
	require.Equal(t, request.ExpectedBoundary, result.Request.Boundary)
	require.Len(t, result.TargetSHA256, 64)
	require.False(t, result.Request.CapturedAt.IsZero())
	bound, err := json.Marshal(result.Request)
	require.NoError(t, err)
	digest := sha256.Sum256(bound)
	require.Equal(t, hex.EncodeToString(digest[:]), result.Inspection.RequestSHA256)
	path := filepath.Join(request.Destination, "inspection.json")
	encoded, err := os.ReadFile(path)
	require.NoError(t, err)
	var retained recovery.ImportInspectionResult
	require.NoError(t, json.Unmarshal(encoded, &retained))
	require.Equal(t, result, retained)
	require.NotContains(t, string(encoded), cfg.Security.MasterKey)
	directory, err := productfiles.ExistingDirectory(request.Destination)
	require.NoError(t, err)
	_, err = directory.ReadFile("inspection.json", 1<<20)
	require.NoError(t, err)
	assertInspectionStillClosed(t, cfg)
	_, err = InspectImportedBackup(t.Context(), cfg, request)
	require.Error(t, err)
}

func TestInspectImportedBackupBindsReplayAndLaterClosedEpoch(t *testing.T) {
	cfg, request, identity := importedInspectionFixture(t)
	store, transfer, err := storage.OpenImportTarget(t.Context(), cfg.RuntimeStorage())
	require.NoError(t, err)
	receipt, err := transfer.(storage.ImportReconciler).ReconcileImport(t.Context(), identity.KVClaim, 1, "", strings.Repeat("a", 64), []storage.CompareAndSwapMutation{{Key: "inspection-fixture", NewValue: []byte("retained")}})
	require.NoError(t, err)
	require.NoError(t, store.Close())
	db, err := openBackupSQL(cfg)
	require.NoError(t, err)
	sqlReceipt, err := db.ReplayRelationalImport(t.Context(), identity.SQLOriginal, identity.SQL, sqlstore.RelationalReplayStep{Sequence: 1, EvidenceSHA256: strings.Repeat("b", 64), TransitionSHA256: strings.Repeat("c", 64)}, func(context.Context, *sql.Conn) error { return nil })
	require.NoError(t, err)
	witness, err := recovery.New(db)
	require.NoError(t, err)
	later, err := witness.PrepareImportedEpoch(t.Context(), recovery.ImportedEpochRequest{Prepared: request.ExpectedBoundary, Snapshot: identity.SQLOriginal, Import: identity.SQL, Evidence: recovery.EpochEvidence{HighestEpoch: 9, SourceSHA256: strings.Repeat("d", 64), Reference: "independent-history", Operator: "operator"}})
	require.NoError(t, err)
	require.NoError(t, db.Close())
	_, err = InspectImportedBackup(t.Context(), cfg, request)
	require.ErrorIs(t, err, recovery.ErrConflict)
	require.NoDirExists(t, request.Destination)
	request.ExpectedBoundary = later
	_, err = InspectImportedBackup(t.Context(), cfg, request)
	require.Error(t, err)
	require.NoFileExists(t, filepath.Join(request.Destination, "inspection.json"))
	request.Destination += "-current"
	request.KVPosition = storage.ImportReplayPosition{Sequence: 1, ReceiptSHA256: receipt}
	request.SQLPosition = sqlstore.RelationalReplayPosition{Sequence: 1, ReceiptSHA256: sqlReceipt}
	result, err := InspectImportedBackup(t.Context(), cfg, request)
	require.NoError(t, err)
	require.Equal(t, later, result.Request.Boundary)
	assertInspectionStillClosed(t, cfg)
}

func TestInspectImportedBackupRefusesChangedIdentities(t *testing.T) {
	cfg, request, _ := importedInspectionFixture(t)
	for name, change := range map[string]func(*recovery.InspectImportRequest){
		"source-digest":      func(r *recovery.InspectImportRequest) { r.ManifestSHA256 = strings.Repeat("0", 64) },
		"operation":          func(r *recovery.InspectImportRequest) { r.Operation.ID = "another" },
		"fencing":            func(r *recovery.InspectImportRequest) { r.Operation.FencingEvidence = "different" },
		"boundary":           func(r *recovery.InspectImportRequest) { r.ExpectedBoundary.Evidence = "different" },
		"deployment":         func(r *recovery.InspectImportRequest) { r.ExpectedBoundary.DeploymentID = "another" },
		"badger-incarnation": func(r *recovery.InspectImportRequest) { r.ValkeyIncarnation = "unexpected" },
		"sql-position": func(r *recovery.InspectImportRequest) {
			r.SQLPosition = sqlstore.RelationalReplayPosition{Sequence: 1, ReceiptSHA256: strings.Repeat("a", 64)}
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := request
			candidate.Destination += "-" + name
			change(&candidate)
			result, err := InspectImportedBackup(t.Context(), cfg, candidate)
			require.Error(t, err)
			require.Zero(t, result)
			require.NoFileExists(t, filepath.Join(candidate.Destination, "inspection.json"))
			assertInspectionStillClosed(t, cfg)
		})
	}
}

func TestInspectImportedBackupRefusesAbsentAndOverlappingTargetsBeforeCreation(t *testing.T) {
	cfg, request, _ := importedInspectionFixture(t)
	for name, change := range map[string]func(*config.Config, *recovery.InspectImportRequest){
		"missing-badger": func(c *config.Config, r *recovery.InspectImportRequest) {
			c.Storage.Badger.Path = filepath.Join(filepath.Dir(r.Destination), "absent-badger")
		},
		"missing-sql": func(c *config.Config, r *recovery.InspectImportRequest) {
			c.Storage.SQL.SQLite.Path = filepath.Join(filepath.Dir(r.Destination), "absent.db")
		},
		"missing-blobs": func(c *config.Config, r *recovery.InspectImportRequest) {
			c.Files.Path = filepath.Join(filepath.Dir(r.Destination), "absent-blobs")
		},
		"output-in-kv": func(c *config.Config, r *recovery.InspectImportRequest) {
			r.Destination = filepath.Join(c.Storage.Badger.Path, "inspection")
		},
		"output-in-blobs": func(c *config.Config, r *recovery.InspectImportRequest) {
			r.Destination = filepath.Join(c.Files.Path, "inspection")
		},
		"output-in-source": func(_ *config.Config, r *recovery.InspectImportRequest) {
			r.Destination = filepath.Join(r.Directory, "inspection")
		},
		"scratch-in-kv":     func(c *config.Config, r *recovery.InspectImportRequest) { r.ScratchDirectory = c.Storage.Badger.Path },
		"scratch-in-blobs":  func(c *config.Config, r *recovery.InspectImportRequest) { r.ScratchDirectory = c.Files.Path },
		"scratch-in-source": func(_ *config.Config, r *recovery.InspectImportRequest) { r.ScratchDirectory = r.Directory },
		"scratch-alias-source": func(_ *config.Config, r *recovery.InspectImportRequest) {
			alias := filepath.Join(filepath.Dir(r.Destination), "backup-alias")
			require.NoError(t, os.Symlink(r.Directory, alias))
			r.ScratchDirectory = alias
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := request
			candidate.Destination += "-" + name
			target := *cfg
			change(&target, &candidate)
			result, err := InspectImportedBackup(t.Context(), &target, candidate)
			require.Error(t, err)
			require.Zero(t, result)
			require.NoDirExists(t, candidate.Destination)
			assertInspectionStillClosed(t, cfg)
		})
	}
	for _, name := range []string{"absent-badger", "absent.db", "absent-blobs"} {
		_, err := os.Lstat(filepath.Join(filepath.Dir(request.Destination), name))
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}

func TestInspectImportedBackupSharedTargetsRequireExactIncarnation(t *testing.T) {
	valkey, postgres, endpoint := os.Getenv("TEST_VALKEY_URL"), os.Getenv("TEST_POSTGRES_URL"), os.Getenv("TEST_BLOB_S3_ENDPOINT")
	if valkey == "" || postgres == "" || endpoint == "" {
		t.Skip("UNVERIFIED: imported graph inspection requires Valkey, PostgreSQL, and object storage")
	}
	cfg, prepare := restoreApplicationFixture(t)
	configureSharedRestore(t, cfg, valkey, postgres, endpoint)
	prepared, err := PrepareBackup(t.Context(), cfg, prepare)
	require.NoError(t, err)
	store, err := storage.OpenValkey(cfg.RuntimeStorage().Valkey)
	require.NoError(t, err)
	t.Cleanup(func() {
		keys, err := store.ScanWithPrefix(context.Background(), "", 10000)
		require.NoError(t, err)
		require.NoError(t, store.BatchDelete(context.Background(), keys))
		require.NoError(t, store.Close())
	})
	incarnation, err := store.(storage.IncarnationProvider).ObserveIncarnation(t.Context())
	require.NoError(t, err)
	request := recovery.InspectImportRequest{VerifyRequest: prepare.VerifyRequest, Operation: prepare.Operation, ExpectedBoundary: prepared.Prepared.Boundary, Destination: filepath.Join(filepath.Dir(prepare.FilesDirectory), "inspection")}
	_, err = InspectImportedBackup(t.Context(), cfg, request)
	require.Error(t, err)
	require.NoDirExists(t, request.Destination)
	request.ValkeyIncarnation = strings.Repeat("0", len(incarnation))
	_, err = InspectImportedBackup(t.Context(), cfg, request)
	require.Error(t, err)
	require.NoDirExists(t, request.Destination)
	request.ValkeyIncarnation = incarnation
	result, err := InspectImportedBackup(t.Context(), cfg, request)
	require.NoError(t, err)
	require.Equal(t, incarnation, result.ValkeyIncarnation)
	assertInspectionStillClosed(t, cfg)
	bytes, err := openBlob(t.Context(), cfg.Files)
	require.NoError(t, err)
	_, err = bytes.Get(t.Context(), "retained")
	require.ErrorIs(t, err, blob.ErrImportRestricted)
}
