package app

import (
	"context"
	legacyjson "encoding/json"
	"encoding/json/v2"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/limits/storedbytes"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"
)

// unprefixedImportExit stops only after the actual native owner acknowledges a write.
// It does not replace claim, expiry, conflict, or import behavior.
type unprefixedImportExit struct {
	storage.RecordTransfer
	completed int
}

func (w *unprefixedImportExit) Import(ctx context.Context, claim []byte, record storage.TransferRecord) error {
	if err := w.RecordTransfer.Import(ctx, claim, record); err != nil {
		return err
	}
	w.completed++
	if w.completed == 3 {
		os.Exit(87)
	}
	return nil
}

// unprefixedChildConfig reconstructs exact library inputs from a private fixture.
// It does not prove loading an operator primary configuration file.
func unprefixedChildConfig(t *testing.T, path string) (*config.Config, RecoveryActivationRequest) {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var fixture activationChildFixture
	require.NoError(t, json.Unmarshal(body, &fixture))
	var saved config.Config
	require.NoError(t, json.Unmarshal(fixture.Config, &saved, legacyjson.FormatDurationAsNano(true)))
	values := map[string]string{"STARPORT_SECURITY_MASTER_KEY": saved.Security.MasterKey, "STARPORT_DEPLOYMENT_ID": fixture.Paths.DeploymentID, "STARPORT_INSTANCE_ID": fixture.Paths.InstanceID}
	for key, value := range fixture.Catalog {
		name := "STARPORT_" + strings.TrimPrefix(key, "STARMAP_")
		if key == "STARMAP_STATE_DIR" {
			name = "STARPORT_CATALOG_STATE_DIR"
		}
		values[name] = value
	}
	cfg, err := config.NewLoader().WithPaths(fixture.Paths).WithEnvFiles().WithEnvironment(values).Load(t.Context())
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(fixture.Config, cfg, legacyjson.FormatDurationAsNano(true)))
	return cfg, fixture.Request
}

func runUnprefixedImportChild(t *testing.T, cfg *config.Config, request RecoveryActivationRequest, mode string) {
	t.Helper()
	body, err := json.Marshal(cfg, legacyjson.FormatDurationAsNano(true), json.Deterministic(true))
	require.NoError(t, err)
	fixture, err := json.Marshal(activationChildFixture{cfg.EffectivePaths(), body, cfg.Catalog.CatalogValues(), request}, json.Deterministic(true))
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "unprefixed-child.json")
	require.NoError(t, os.WriteFile(path, fixture, 0600))
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestUnprefixedMigrationPreservesRecordsAcrossProcessExit$", "-test.v")
	command.Env = append(os.Environ(), "STARPORT_UNPREFIXED_CHILD="+path)
	command.Env = append(command.Env, "STARPORT_UNPREFIXED_MODE="+mode)
	output, processErr := command.CombinedOutput()
	evidence := os.Getenv("STARPORT_UNPREFIXED_EVIDENCE_DIR")
	if evidence == "" {
		evidence = filepath.Join(t.TempDir(), "private-child-output")
		_, err := productfiles.NewDirectory(evidence)
		require.NoError(t, err)
	}
	_, err = productfiles.ExistingDirectory(evidence)
	require.NoError(t, err)
	log, err := os.CreateTemp(evidence, "unprefixed-child-*.log")
	require.NoError(t, err)
	_, writeErr := log.Write(output)
	require.NoError(t, errors.Join(writeErr, log.Close()))
	t.Logf("private child output: %s", log.Name())
	if mode == "cut" {
		require.Error(t, processErr, "inspect private child output: %s", log.Name())
		require.NotNil(t, command.ProcessState)
		require.Equal(t, 87, command.ProcessState.ExitCode(), "inspect private child output: %s", log.Name())
	} else {
		require.NoError(t, processErr, "inspect private child output: %s", log.Name())
	}
}

func checkUnprefixedImportedRecords(t *testing.T, cfg *config.Config, prepare recovery.PrepareRequest, expected []storage.TransferRecord, count int) {
	t.Helper()
	source, _, _, err := inspectBackupRestore(t.Context(), cfg, prepare)
	require.NoError(t, err)
	identity, err := source.ImportIdentity(prepare.Operation)
	require.NoError(t, err)
	store, transfer, err := storage.OpenImportTarget(t.Context(), cfg.RuntimeStorage())
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	want := make(map[string]storage.TransferRecord, len(expected))
	for _, record := range expected {
		want[record.Key] = record
	}
	found := make(map[string]storage.TransferRecord)
	require.NoError(t, transfer.(storage.ImportInspector).InspectImport(t.Context(), identity.KVClaim, storage.ImportReplayPosition{}, func(record storage.TransferRecord) error {
		require.Equal(t, want[record.Key], record, "native partial import must preserve original bytes and absolute expiry")
		found[record.Key] = record
		return nil
	}))
	require.Len(t, found, count)
}

func checkUnprefixedRecoveredActivity(t *testing.T, cfg *config.Config, activity *postBackupActivity) {
	t.Helper()
	store, err := storage.Open(cfg.RuntimeStorage())
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	db, err := sqlstore.Open(cfg.Storage.RuntimeSQL())
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	witness, err := recovery.New(db)
	require.NoError(t, err)
	authority, err := witness.OpenAuthority(t.Context(), store.(storage.IncarnationProvider), cfg.EffectivePaths().DeploymentID)
	require.NoError(t, err)
	ledger, err := reservation.Open(authority)
	require.NoError(t, err)
	window, err := ledger.Window(t.Context(), activity.meter, activity.through)
	require.NoError(t, err)
	require.EqualValues(t, 700, window.Consumed)
	require.EqualValues(t, 200, window.Reserved)
	for _, expected := range activity.attempts {
		actual, err := ledger.Inspect(t.Context(), expected.Attempt.ID)
		require.NoError(t, err)
		require.Equal(t, expected, *actual)
	}
	require.ErrorIs(t, ledger.Begin(t.Context(), "post-backup-uncertain"), reservation.ErrAlreadyDispatched)
	keys, err := apikey.Open(store)
	require.NoError(t, err)
	_, err = keys.GetByID(t.Context(), activity.key.ID)
	require.ErrorIs(t, err, apikey.ErrNotFound)
	_, err = keys.GetByHash(t.Context(), activity.key.Hash)
	require.ErrorIs(t, err, apikey.ErrNotFound)
	people, err := identity.Open(db)
	require.NoError(t, err)
	reachable, err := people.AccountGrants.ReachableAccounts(t.Context(), activity.grant.UserID)
	require.NoError(t, err)
	require.Empty(t, reachable)
	repository, err := jobs.OpenRepository(store)
	require.NoError(t, err)
	job, err := repository.Get(t.Context(), postBackupAccount, "post-backup-job")
	require.NoError(t, err)
	require.True(t, job.SubmissionPending)
	service, err := jobs.NewService(repository, jobs.WithRequiredSettlement(&budgetOwner{ledger: ledger}))
	require.NoError(t, err)
	_, err = service.Refresh(t.Context(), activity.runner, postBackupAccount, job.ID)
	require.ErrorIs(t, err, jobs.ErrSubmissionUnconfirmed)
	_, err = service.Cancel(t.Context(), activity.runner, postBackupAccount, job.ID)
	require.ErrorIs(t, err, jobs.ErrSubmissionUnconfirmed)
	require.Equal(t, 1, activity.runner.submits)
	require.Zero(t, activity.runner.polls)
	require.Zero(t, activity.runner.cancels)
	require.Zero(t, activity.runner.fetches)
}

func TestUnprefixedMigrationPreservesRecordsAcrossProcessExit(t *testing.T) {
	if path := os.Getenv("STARPORT_UNPREFIXED_CHILD"); path != "" {
		cfg, request := unprefixedChildConfig(t, path)
		prepare := request.Prepare
		if os.Getenv("STARPORT_UNPREFIXED_MODE") == "activate" {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			result, err := ActivateRecovery(ctx, cfg, request)
			require.NoError(t, err)
			require.Equal(t, 3, result.CompletedPhases)
			require.True(t, result.HistoricallyComplete)
			require.True(t, result.CurrentAdmissionValid)
			require.False(t, result.Restricted)
			require.Equal(t, request.ExpectedDecisionSHA256, result.DecisionSHA256)
			return
		}
		if os.Getenv("STARPORT_UNPREFIXED_MODE") == "cut" {
			source, _, _, err := inspectBackupRestore(t.Context(), cfg, prepare)
			require.NoError(t, err)
			db, err := sqlstore.Open(cfg.Storage.RuntimeSQL())
			require.NoError(t, err)
			require.NoError(t, db.PrepareImportSchema(t.Context()))
			store, transfer, err := storage.OpenImportTarget(t.Context(), cfg.RuntimeStorage())
			require.NoError(t, err)
			_ = store
			blobs, err := restoreBlobTarget(t.Context(), cfg.Files)
			require.NoError(t, err)
			_, err = source.Prepare(t.Context(), recovery.BundleTargets{KV: &unprefixedImportExit{RecordTransfer: transfer}, SQL: db, Blobs: blobs, FilesDirectory: prepare.FilesDirectory}, prepare.Operation)
			require.NoError(t, err)
			t.Fatal("native import did not reach its process-exit boundary")
		}
		prepared, err := PrepareBackup(t.Context(), cfg, prepare)
		require.NoError(t, err)
		require.False(t, prepared.Prepared.Boundary.Open)
		_, err = storage.Open(cfg.RuntimeStorage())
		require.ErrorIs(t, err, storage.ErrImportRestricted)
		return
	}
	address, postgres, endpoint := os.Getenv("TEST_UNPREFIXED_VALKEY_URL"), os.Getenv("TEST_POSTGRES_URL"), os.Getenv("TEST_BLOB_S3_ENDPOINT")
	if address == "" || postgres == "" || endpoint == "" {
		t.Skip("UNVERIFIED: namespace recovery requires dedicated source Valkey, PostgreSQL, and object storage")
	}
	options, err := valkey.ParseURL(address)
	require.NoError(t, err)
	options.DisableCache, options.ForceSingleClient = true, true
	raw, err := valkey.NewClient(options)
	require.NoError(t, err)
	t.Cleanup(raw.Close)
	count, err := raw.Do(t.Context(), raw.B().Dbsize().Build()).AsInt64()
	require.NoError(t, err)
	require.Zero(t, count, "unprefixed source fixture must be an empty dedicated database")
	var activity postBackupActivity
	var captured recovery.CaptureResult
	var sourceConfig *config.Config
	var records []storage.TransferRecord
	var file files.File
	cfg, prepare := boundedActivationSourceFixtureWithActivity(t, false, func(source *config.Config) {
		activity.beforeCapture(t, source)
		store, err := storage.Open(source.RuntimeStorage())
		require.NoError(t, err)
		meter, err := storedbytes.NewStorageMeter(store)
		require.NoError(t, err)
		repository, err := files.OpenRepository(store)
		require.NoError(t, err)
		blobs, err := openBlob(t.Context(), source.Files)
		require.NoError(t, err)
		service, err := files.NewService(repository, blobs, files.WithMeter(meter))
		require.NoError(t, err)
		require.NoError(t, meter.InitializeEmpty(t.Context(), postBackupAccount))
		file, err = service.Upload(t.Context(), files.UploadRequest{Account: postBackupAccount, Filename: "migration.txt", Purpose: files.PurposeUserData}, strings.NewReader("original linked bytes"))
		require.NoError(t, err)
		require.NoError(t, store.Close())
	}, func(source *config.Config, original recovery.CaptureResult) {
		activity.afterCapture(t, source, original)
		store, err := storage.Open(source.RuntimeStorage())
		require.NoError(t, err)
		transfer, err := storage.OpenRecordTransfer(t.Context(), store, "")
		require.NoError(t, err)
		require.NoError(t, transfer.Enumerate(t.Context(), func(record storage.TransferRecord) error {
			command := raw.B().Set().Key(record.Key).Value(string(record.Value))
			if record.ExpiresAtMillis == 0 {
				err = raw.Do(t.Context(), command.Build()).Error()
			} else {
				err = raw.Do(t.Context(), command.PxatMillisecondsTimestamp(record.ExpiresAtMillis).Build()).Error()
			}
			require.NoError(t, err)
			records = append(records, record)
			t.Cleanup(func() {
				require.NoError(t, raw.Do(context.Background(), raw.B().Del().Key(record.Key).Build()).Error())
			})
			return nil
		}))
		require.NoError(t, store.Close())
		source.Storage.Mode = storage.StorageTypeValkey
		source.Storage.Valkey.URL, source.Storage.Valkey.AllowInsecure = address, true
		sourceConfig = source
		captured, err = CaptureBackup(t.Context(), source, recovery.CaptureRequest{Destination: original.Directory + "-unprefixed", Build: "test", OperationID: "unprefixed-capture", FencingEvidence: "source-writers-stopped", KeyReference: "test-master-key", UnprefixedValkey: true})
		require.NoError(t, err)
	})
	prepare.Directory, prepare.ManifestSHA256 = captured.Directory, captured.ManifestSHA256
	mixed := "{starport:v1:foreign:}kv:account"
	require.NoError(t, raw.Do(t.Context(), raw.B().Set().Key(mixed).Value("foreign namespace bytes").Build()).Error())
	t.Cleanup(func() { require.NoError(t, raw.Do(context.Background(), raw.B().Del().Key(mixed).Build()).Error()) })
	_, err = CaptureBackup(t.Context(), sourceConfig, recovery.CaptureRequest{Destination: captured.Directory + "-mixed", Build: "test", OperationID: "mixed-refusal", FencingEvidence: "source-writers-stopped", KeyReference: "test-master-key", UnprefixedValkey: true})
	require.ErrorContains(t, err, "dedicated source database")
	foreignBytes, err := raw.Do(t.Context(), raw.B().Get().Key(mixed).Build()).AsBytes()
	require.NoError(t, err)
	require.Equal(t, "foreign namespace bytes", string(foreignBytes))
	require.NoError(t, raw.Do(t.Context(), raw.B().Del().Key(mixed).Build()).Error())
	require.True(t, captured.UnprefixedValkey)
	require.EqualValues(t, len(records), captured.KVRecords)
	require.Positive(t, captured.References.FileRecords)
	require.Positive(t, captured.References.StoredByteClaims)
	require.Positive(t, captured.References.BudgetRecords)
	require.Positive(t, captured.References.AccountRecords)
	require.Positive(t, captured.References.JobRecords)
	require.Positive(t, captured.References.HeldReservations)
	verified, err := VerifyBackup(t.Context(), cfg, prepare.VerifyRequest)
	require.NoError(t, err)
	require.Equal(t, captured, verified)
	parsed, err := url.Parse(address)
	require.NoError(t, err)
	require.Equal(t, "/13", parsed.Path)
	parsed.Path = "/14"
	configureSharedRestore(t, cfg, parsed.String(), postgres, endpoint)
	t.Cleanup(func() {
		store, err := storage.OpenValkey(cfg.RuntimeStorage().Valkey)
		require.NoError(t, err)
		keys, err := store.ScanWithPrefix(context.Background(), "", 10000)
		require.NoError(t, err)
		if len(keys) > 0 {
			require.NoError(t, store.BatchDelete(context.Background(), keys))
		}
		require.NoError(t, store.Close())
	})
	runUnprefixedImportChild(t, cfg, RecoveryActivationRequest{Prepare: prepare}, "cut")
	checkUnprefixedImportedRecords(t, cfg, prepare, records, 3)
	_, err = storage.Open(cfg.RuntimeStorage())
	require.ErrorIs(t, err, storage.ErrImportRestricted)
	db, err := sqlstore.Open(cfg.Storage.RuntimeSQL())
	require.NoError(t, err)
	require.ErrorIs(t, db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
	require.NoError(t, db.Close())
	runUnprefixedImportChild(t, cfg, RecoveryActivationRequest{Prepare: prepare}, "prepare")
	prepared, err := PrepareBackup(t.Context(), cfg, prepare)
	require.NoError(t, err)
	require.EqualValues(t, len(records), prepared.Prepared.KV.ProcessedRecords)
	checkUnprefixedImportedRecords(t, cfg, prepare, records, len(records))
	foreign := prepare
	foreign.Operation.ID = "foreign-import-owner"
	_, err = PrepareBackup(t.Context(), cfg, foreign)
	require.Error(t, err)
	ordinary, err := storage.Open(sourceConfig.RuntimeStorage())
	require.NoError(t, err)
	for _, record := range records {
		_, err = ordinary.Get(t.Context(), record.Key)
		require.ErrorIs(t, err, storage.ErrNotFound, "ordinary source must not read the raw layout")
	}
	require.NoError(t, ordinary.Close())
	cfg, activation := activationPreparedFixture(t, cfg, prepare)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	unsafe := activation
	unsafe.History.Attestation.WritersFenced = false
	_, err = ActivateRecovery(ctx, cfg, unsafe)
	require.Error(t, err)
	sealed, native := sealActivationFixture(t, cfg, activation)
	activation.ExpectedDecisionSHA256 = sealed.journal.Digest()
	commitActivationNativePhases(t, cfg, sealed, native, 1)
	require.NoError(t, native.close())
	runUnprefixedImportChild(t, cfg, activation, "activate")
	status, err := InspectRecoveryActivation(ctx, cfg, activation)
	require.NoError(t, err)
	require.True(t, status.HistoricallyComplete)
	require.True(t, status.CurrentAdmissionValid)
	checkUnprefixedRecoveredActivity(t, cfg, &activity)
	store, err := storage.Open(cfg.RuntimeStorage())
	require.NoError(t, err)
	keys, err := apikey.Open(store)
	require.NoError(t, err)
	retainedKey, err := keys.GetByID(t.Context(), activity.survivor.ID)
	require.NoError(t, err)
	require.Equal(t, activity.survivor.ID, retainedKey.APIKey.ID)
	meter, err := storedbytes.NewStorageMeter(store)
	require.NoError(t, err)
	total, err := meter.Total(t.Context(), postBackupAccount)
	require.NoError(t, err)
	require.Equal(t, file.Bytes, total)
	repository, err := files.OpenRepository(store)
	require.NoError(t, err)
	blobs, err := openBlob(t.Context(), cfg.Files)
	require.NoError(t, err)
	service, err := files.NewService(repository, blobs, files.WithMeter(meter))
	require.NoError(t, err)
	_, reader, err := service.Open(t.Context(), postBackupAccount, file.ID)
	require.NoError(t, err)
	payload, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, "original linked bytes", string(payload))
	require.NoError(t, store.Close())
	for _, record := range records {
		value, err := raw.Do(t.Context(), raw.B().Get().Key(record.Key).Build()).AsBytes()
		require.NoError(t, err)
		require.Equal(t, record.Value, value)
		expiry, err := raw.Do(t.Context(), raw.B().Pexpiretime().Key(record.Key).Build()).AsInt64()
		require.NoError(t, err)
		if expiry == -1 {
			expiry = 0
		}
		require.Equal(t, record.ExpiresAtMillis, expiry)
	}
	count, err = raw.Do(t.Context(), raw.B().Dbsize().Build()).AsInt64()
	require.NoError(t, err)
	require.Equal(t, captured.KVRecords, count)
	// The original source remains closed after the explicit destination switch.
	db, err = sqlstore.Open(sourceConfig.Storage.RuntimeSQL())
	require.NoError(t, err)
	witness, err := recovery.New(db)
	require.NoError(t, err)
	boundary, err := witness.Current(t.Context(), sourceConfig.EffectivePaths().DeploymentID)
	require.NoError(t, err)
	require.False(t, boundary.Open)
	_, err = witness.Approved(t.Context(), sourceConfig.EffectivePaths().DeploymentID)
	require.ErrorIs(t, err, recovery.ErrClosed)
	require.NoError(t, db.Close())
	db, err = sqlstore.Open(cfg.Storage.RuntimeSQL())
	require.NoError(t, err)
	witness, err = recovery.New(db)
	require.NoError(t, err)
	approved, err := witness.Approved(t.Context(), cfg.EffectivePaths().DeploymentID)
	require.NoError(t, err)
	require.True(t, approved.Open)
	require.Greater(t, approved.Epoch, boundary.Epoch)
	require.NoError(t, db.Close())
}
