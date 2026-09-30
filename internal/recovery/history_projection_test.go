package recovery

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

const (
	historyProjectionAccount        = "owner"
	historyProjectionPerson         = "person"
	historyProjectionUnitPrice      = "0.000000001"
	historyProjectionAsset          = "assets/000001.bin"
	historyProjectionBlobAbsent     = "absent"
	historyProjectionDomain         = "kv_domain"
	historyProjectionMissingAttempt = "missing-attempt"
	historyProjectionChangedOutput  = "changed-output"
	historyProjectionEmptyKey       = "empty"
	historyProjectionChangedArchive = "changed-archive"
)

const (
	historyProjectionSharpPointerFormat = "%#p"
	historyProjectionPlusPointerFormat  = "%+p"
	historyProjectionSharpValueFormat   = "%#v"
	historyProjectionPlusValueFormat    = "%+v"
	historyProjectionOffering           = "provider/model"
	historyProjectionAssetID            = "independent-file"
	historyProjectionBlobKind           = "blob_publication"
	historyProjectionExtraFile          = "extra-file"
	historyProjectionChangedKV          = "changed-kv"
	historyProjectionMissingArchive     = "missing-archive"
	historyProjectionMissingSQL         = "missing-sql"
	historyProjectionPartial            = "partial"
	historyProjectionForgedBinding      = "forged-binding"
	historyProjectionStaleSQL           = "stale-final-sql"
	historyProjectionCreatedEmpty       = "created-empty"
	historyProjectionExpired            = "expired"
	historyProjectionPersistent         = "persistent"
	historyProjectionExpirationKey      = "retained-expiration"
	historyProjectionUnrecordedKey      = "unrecorded"
	historyProjectionEvidenceID         = "source"
	historyProjectionCaptureDirectory   = "captured"
	historyProjectionAssetFile          = "000001.bin"
	historyProjectionAssetDirectory     = "assets"
	historyProjectionPayloadDirectory   = "payloads"
	historyProjectionPayloadPattern     = "payloads/%06d.json"
	historyProjectionConfigurationFile  = "config.env"
	historyProjectionFileDirectory      = "files"
)

var historyProjectionPrivacyFormats = []string{"%v", historyProjectionPlusValueFormat, historyProjectionSharpValueFormat, "%s", "%p", historyProjectionPlusPointerFormat, historyProjectionSharpPointerFormat}

type historyProjectionFixture struct {
	source, captured *RestoreSource
	bundle           BundleSources
	capture          BundleRequest
	request          HistoryProjectionRequest
	kv               storage.KVStore
	manifest         historyManifest
	attempt          reservation.Attempt
}

func captureHistoryProjectionBundle(t *testing.T, native BundleSources, capture BundleRequest) *RestoreSource {
	t.Helper()
	directory := filepath.Join(privateKVDirectory(t), historyProjectionCaptureDirectory)
	manifest, err := BackupBundle(t.Context(), directory, native, capture)
	require.NoError(t, err)
	digest, err := manifest.Digest()
	require.NoError(t, err)
	source, err := InspectRestoreSource(t.Context(), VerifyRequest{Directory: directory, ManifestSHA256: digest}, native.Encryption)
	require.NoError(t, err)
	return source
}

func independentHistoryProjectionFixture(t *testing.T) historyProjectionFixture {
	t.Helper()
	bundle, capture, _ := backupBundleFixture(t)
	kv, records, _ := kvTransferStores(t, storage.StorageTypeBadger)
	bundle.KV = records
	accounts, err := account.Open(kv)
	require.NoError(t, err)
	initial, err := accounts.Create(t.Context(), account.Account{ID: historyProjectionAccount, Name: "Original owner", Active: true})
	require.NoError(t, err)
	require.NoError(t, kv.Set(t.Context(), historyProjectionEmptyKey, []byte{}))
	require.NoError(t, kv.SetWithTTL(t.Context(), historyProjectionExpirationKey, []byte("original deadline"), time.Hour))
	people, err := identity.Open(bundle.SQL)
	require.NoError(t, err)
	_, err = people.Users.Create(t.Context(), identity.User{ID: historyProjectionPerson, Subject: "private-independent-subject"})
	require.NoError(t, err)
	grant, err := people.AccountGrants.Add(t.Context(), identity.AccountGrant{AccountID: historyProjectionAccount, UserID: historyProjectionPerson})
	require.NoError(t, err)
	budget, err := reservation.Open(kv.(storage.TimeBoundStore))
	require.NoError(t, err)
	at := time.Now().UTC()
	meter := reservation.Meter{Scope: limits.ScopeAccount, Holder: historyProjectionAccount, Dimension: limits.DimensionSpend, Interval: limits.IntervalDay}
	require.NoError(t, budget.EstablishWindow(t.Context(), meter, at, 0, reservation.History{ID: "independent-window", Proof: "fixture-owned-window-evidence"}))
	attempt := reservation.Attempt{ID: "uncertain-attempt", RequestID: "original-request", AccountID: historyProjectionAccount, KeyID: "fixture-key", OfferingID: historyProjectionOffering, CatalogGeneration: "fixture-generation", Operation: string(routing.OperationChatCompletions), Rules: []reservation.Rule{{Meter: meter, Limit: 1000, PolicyRevision: "original-policy", HistoryID: "independent-window"}}, Valuation: reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: "tokens", Price: reservation.Price{USD: historyProjectionUnitPrice, PerUnits: 1}}}}, Bound: reservation.Quantities{"tokens": 200}}
	_, err = budget.Reserve(t.Context(), attempt)
	require.NoError(t, err)
	source := captureHistoryProjectionBundle(t, bundle, capture)
	before, err := source.OpenCapturedKV(t.Context())
	require.NoError(t, err)
	originalAccount, err := account.CaptureRecoveryRecord(t.Context(), before, historyProjectionAccount)
	require.NoError(t, err)
	require.NoError(t, before.Close())
	initial.Account.Active = false
	_, err = accounts.Update(t.Context(), initial.Account, initial.Revision)
	require.NoError(t, err)
	require.NoError(t, people.AccountGrants.Remove(t.Context(), grant))
	require.NoError(t, budget.Begin(t.Context(), attempt.ID))
	require.NoError(t, budget.MarkUncertain(t.Context(), attempt.ID, "independent-lost-provider-response"))
	repository, err := files.OpenRepository(kv)
	require.NoError(t, err)
	service, err := files.NewService(repository, bundle.Blobs)
	require.NoError(t, err)
	data := []byte("independent file bytes")
	uploaded, err := service.Upload(t.Context(), files.UploadRequest{Account: historyProjectionAccount, Filename: "retained.txt", Purpose: files.PurposeUserData, Size: int64(len(data))}, strings.NewReader(string(data)))
	require.NoError(t, err)
	after := historyPayloadView(t, records)
	finalAccount, err := account.CaptureRecoveryRecord(t.Context(), after, historyProjectionAccount)
	require.NoError(t, err)
	held, err := reservation.ReadBackupAttempt(t.Context(), after, attempt.ID)
	require.NoError(t, err)
	file, err := files.CaptureRecoveryFile(t.Context(), after, historyProjectionAccount, uploaded.ID)
	require.NoError(t, err)
	var address struct {
		BlobKey string `json:"blob_key"`
	}
	fileBytes, err := file.MarshalJSON()
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(fileBytes, &address))
	require.NotEmpty(t, address.BlobKey)
	_, kvExpected, err := revision.CaptureKVRecovery(t.Context(), after)
	require.NoError(t, err)
	conn, err := bundle.SQL.Conn(t.Context())
	require.NoError(t, err)
	sqlExpected, err := revision.CaptureSQLRecovery(t.Context(), bundle.SQL, conn)
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	withdraw, err := identity.NewRecoveryTransition([]identity.RecoveryIdentityEvent{{Grant: &identity.RecoveryGrantChange{Before: &grant}}})
	require.NoError(t, err)
	_, historyRequest, history := historyPackageFixture(t)
	prepared, err := source.ImportIdentity(historyRequest.Operation)
	require.NoError(t, err)
	preparedBytes, err := json.Marshal(prepared, json.Deterministic(true))
	require.NoError(t, err)
	history.BackupSHA256, history.DeploymentID, history.PreparedSHA256 = source.ManifestDigest(), source.DeploymentID(), historySHA256(preparedBytes)
	history.Mode, history.Disposition = "disaster_recovery", historyReplayComplete
	history.Interval.Through = time.Now().UTC()
	history.HighestEpoch.HighestEpoch = source.manifest.Request.Boundary.Epoch
	history.Evidence[0].SHA256, history.Evidence[0].Size = historySHA256(fileBytes), int64(len(fileBytes))
	history.HighestEpoch.SourceSHA256 = history.Evidence[0].SHA256
	require.NoError(t, os.Mkdir(filepath.Join(historyRequest.Directory, historyProjectionAssetDirectory), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(historyRequest.Directory, historyProjectionAssetDirectory, historyProjectionAssetFile), data, 0o600))
	history.Assets = []historyAsset{{ID: historyProjectionAssetID, Path: historyProjectionAsset, Size: int64(len(data)), SHA256: historySHA256(data), Evidence: []string{historyProjectionEvidenceID}}}
	inputs := []historyFixturePayload{
		{historyProjectionBlobKind, historyBlobPayload{Version: 1, Key: address.BlobKey, Expected: blob.PublicationState{Kind: historyProjectionBlobAbsent}, Next: blob.PublicationState{Kind: historyProjectionBlobLive, Size: int64(len(data)), SHA256: historySHA256(data)}, AssetID: historyProjectionAssetID}},
		{historyProjectionDomain, historyKVDomain{Version: 1, Accounts: []account.RecoveryChange{{ID: historyProjectionAccount, ExpectedSHA256: originalAccount.SHA256(), Next: &finalAccount}}, Attempts: []reservation.Record{*held}, Files: []files.RecoveryChange{{Account: historyProjectionAccount, ID: uploaded.ID, After: &file}}}},
		{historySQLIdentity, withdraw},
		{historyKVAuthorityFinal, historyKVAuthorityPayload{Version: 1, ExpectedSHA256: kvExpected}},
		{historySQLAuthorityFinal, historySQLAuthorityPayload{Version: 1, Expected: sqlExpected}},
	}
	for i, input := range inputs {
		body := historyPayloadJSON(t, input.value)
		path := fmt.Sprintf(historyProjectionPayloadPattern, i+1)
		require.NoError(t, os.WriteFile(filepath.Join(historyRequest.Directory, path), body, 0o600))
		history.Steps = append(history.Steps, historyStep{Ordinal: i + 1, Kind: input.kind, Path: path, Size: len(body), SHA256: historySHA256(body), Evidence: []string{historyProjectionEvidenceID}})
	}
	historyRequest = writeHistoryManifest(t, historyRequest, history)
	return historyProjectionFixture{source: source, captured: captureHistoryProjectionBundle(t, bundle, capture), bundle: bundle, capture: capture, kv: kv, manifest: history, attempt: attempt, request: HistoryProjectionRequest{Directory: privateKVDirectory(t), History: historyRequest, Attestation: HistoryAttestation{Operator: "private-projection-operator", Reference: "independent-complete-interval", WritersFenced: true, AdmittedWorkAccounted: true, CompleteInterval: true}}}
}

func TestIndependentHistoryProjectionMatchesActualPostBackupOwners(t *testing.T) {
	f := independentHistoryProjectionFixture(t)
	projection, err := ProjectIndependentHistory(t.Context(), f.source, f.request, f.bundle.Encryption)
	require.NoError(t, err)
	compared, err := projection.CompareCaptured(t.Context(), f.captured)
	require.NoError(t, err)
	require.True(t, compared.Restricted)
	require.True(t, compared.RequiresAuthority && compared.RequiresNativeClaim && compared.RequiresCatalog && compared.RequiresFiles && compared.RequiresFencing)
	require.Equal(t, f.manifest.Interval.Through, compared.Through)
	require.Equal(t, f.captured.ManifestDigest(), compared.CapturedSHA256)
	_, err = projection.CompareCaptured(t.Context(), f.source)
	require.Error(t, err, "old backup must not substitute for independently acknowledged changes")
	view, err := OpenKVSnapshot(t.Context(), KVSnapshotPath(filepath.Join(f.request.Directory, historyProjectionKVDirectory)), f.request.Directory, projection.state.record.KV)
	require.NoError(t, err)
	accountState, err := account.ReadRecoveryAccount(t.Context(), view, historyProjectionAccount)
	require.NoError(t, err)
	require.False(t, accountState.Account.Active)
	held, err := reservation.ReadBackupAttempt(t.Context(), view, f.attempt.ID)
	require.NoError(t, err)
	require.Equal(t, reservation.Uncertain, held.State)
	expiration, err := view.ReadCaptured(t.Context(), historyProjectionExpirationKey, 128)
	require.NoError(t, err)
	require.Positive(t, expiration.ExpiresAtMillis)
	original, err := f.source.OpenCapturedKV(t.Context())
	require.NoError(t, err)
	oldExpiration, err := original.ReadCaptured(t.Context(), historyProjectionExpirationKey, 128)
	require.NoError(t, err)
	require.Equal(t, oldExpiration, expiration)
	_, oldRevision, err := revision.CaptureKVRecovery(t.Context(), original)
	require.NoError(t, err)
	_, retainedRevision, err := revision.CaptureKVRecovery(t.Context(), view)
	require.NoError(t, err)
	require.Equal(t, oldRevision, retainedRevision, "projection must not perform final rotations or renew authority")
	require.NotEqual(t, oldRevision, projection.state.binding.KVRevision)
	require.NoError(t, original.Close())
	require.NoError(t, view.Close())
	again, err := ProjectIndependentHistory(t.Context(), f.source, f.request, f.bundle.Encryption)
	require.NoError(t, err)
	require.Equal(t, projection.state.bytes, again.state.bytes)
	require.Equal(t, projection.state.bindBytes, again.state.bindBytes)
	_, err = again.CompareCaptured(t.Context(), f.captured)
	require.NoError(t, err)
	for _, verb := range historyProjectionPrivacyFormats {
		require.NotContains(t, fmt.Sprintf(verb, *projection), f.request.Directory)
		require.NotContains(t, fmt.Sprintf(verb, *projection), "private-projection-operator")
	}
}

func TestIndependentHistoryProjectionRefusesLossAndUnrecordedChanges(t *testing.T) {
	for _, name := range []string{historyProjectionMissingAttempt, "changed-empty", "unrecorded-domain", "unrecorded-sql", "lost-file", "changed-kv-revision", "missing-kv-revision", "changed-sql-revision", "missing-sql-revision", "unsupported-catalog"} {
		t.Run(name, func(t *testing.T) {
			f := independentHistoryProjectionFixture(t)
			projection, err := ProjectIndependentHistory(t.Context(), f.source, f.request, f.bundle.Encryption)
			require.NoError(t, err)
			switch name {
			case historyProjectionMissingAttempt:
				keys, err := f.kv.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
				require.NoError(t, err)
				require.Len(t, keys, 1)
				require.NoError(t, f.kv.Delete(t.Context(), keys[0]))
			case "changed-empty":
				require.NoError(t, f.kv.Set(t.Context(), historyProjectionEmptyKey, []byte("changed acknowledged bytes")))
			case "unrecorded-domain":
				require.NoError(t, f.kv.Set(t.Context(), "new-unrecorded-owner", []byte(historyProjectionUnrecordedKey)))
			case "unsupported-catalog":
				require.NoError(t, f.kv.Set(t.Context(), "catalog:unrecorded-publication", []byte("new unbound catalog bytes")))
			case "unrecorded-sql":
				_, err := f.bundle.SQL.ExecContext(t.Context(), "INSERT INTO sqlstore_meta(name,value) VALUES('unrecorded-owner','acknowledged')")
				require.NoError(t, err)
			case "lost-file":
				require.NoError(t, f.bundle.Blobs.Retire(t.Context(), "file-one"))
			case "changed-kv-revision":
				require.NoError(t, f.kv.Set(t.Context(), revision.StorageKey, []byte(`{"epoch":"unrecorded-epoch","sequence":1}`)))
			case "missing-kv-revision":
				require.NoError(t, f.kv.Delete(t.Context(), revision.StorageKey))
			case "changed-sql-revision":
				_, err := f.bundle.SQL.ExecContext(t.Context(), "UPDATE authorization_revision SET sequence=sequence+1")
				require.NoError(t, err)
			case "missing-sql-revision":
				_, err := f.bundle.SQL.ExecContext(t.Context(), "DELETE FROM authorization_revision")
				require.NoError(t, err)
			}
			if name == historyProjectionMissingAttempt {
				directory := filepath.Join(privateKVDirectory(t), "incomplete")
				manifest, err := BackupBundle(t.Context(), directory, f.bundle, f.capture)
				require.NoError(t, err)
				digest, err := manifest.Digest()
				require.NoError(t, err)
				_, err = InspectRestoreSource(t.Context(), VerifyRequest{Directory: directory, ManifestSHA256: digest}, f.bundle.Encryption)
				require.Error(t, err, "the captured-state owner must reject missing accounting evidence before comparison")
				return
			}
			captured := captureHistoryProjectionBundle(t, f.bundle, f.capture)
			_, err = projection.CompareCaptured(t.Context(), captured)
			require.Error(t, err)
			_, err = projection.CompareCaptured(t.Context(), f.captured)
			require.NoError(t, err, "changed live state cannot replace retained original projection")
		})
	}
}

func TestIndependentHistoryProjectionRefusesChangedEvidenceAndPartialWorkspace(t *testing.T) {
	for _, name := range []string{historyProjectionPartial, historyProjectionChangedOutput, historyProjectionExtraFile, historyProjectionForgedBinding, "changed-history", "missing-final", "stale-final-kv", historyProjectionStaleSQL, "substituted-operation", "missing-assets", "cancel"} {
		t.Run(name, func(t *testing.T) {
			f := independentHistoryProjectionFixture(t)
			request := f.request
			switch name {
			case "changed-history":
				require.NoError(t, os.WriteFile(filepath.Join(request.History.Directory, historyProjectionPayloadDirectory, "000002.json"), []byte(`{"version":2}`), 0o600))
			case "missing-final":
				f.manifest.Steps = f.manifest.Steps[:len(f.manifest.Steps)-1]
				request.History = writeHistoryManifest(t, request.History, f.manifest)
			case "stale-final-kv", historyProjectionStaleSQL:
				index := len(f.manifest.Steps) - 2
				body := []byte(`{"version":1,"expected_sha256":""}`)
				if name == historyProjectionStaleSQL {
					index++
					body = []byte(`{"version":1,"expected":null}`)
				}
				step := &f.manifest.Steps[index]
				step.Size, step.SHA256 = len(body), historySHA256(body)
				require.NoError(t, os.WriteFile(filepath.Join(request.History.Directory, step.Path), body, 0o600))
				request.History = writeHistoryManifest(t, request.History, f.manifest)
			case "substituted-operation":
				request.History.Operation.ID += "-alternate-operation"
			case "missing-assets":
				require.NoError(t, os.Remove(filepath.Join(request.History.Directory, historyProjectionAssetDirectory, historyProjectionAssetFile)))
			}
			ctx := t.Context()
			if name == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			projection, err := ProjectIndependentHistory(ctx, f.source, request, f.bundle.Encryption)
			if strings.HasPrefix(name, "stale-final") {
				require.NoError(t, err)
				_, err = projection.CompareCaptured(t.Context(), f.captured)
				require.Error(t, err)
				return
			}
			if name == historyProjectionPartial || name == historyProjectionChangedOutput || name == historyProjectionExtraFile || name == historyProjectionForgedBinding {
				require.NoError(t, err)
				switch name {
				case historyProjectionPartial:
					require.NoError(t, os.Remove(filepath.Join(request.Directory, historyProjectionRecordFile)))
				case historyProjectionChangedOutput:
					require.NoError(t, os.WriteFile(KVSnapshotPath(filepath.Join(request.Directory, historyProjectionKVDirectory)), []byte("corrupt retained image"), 0o600))
				case historyProjectionExtraFile:
					require.NoError(t, os.WriteFile(filepath.Join(request.Directory, historyProjectionUnrecordedKey), []byte("unknown state"), 0o600))
				case historyProjectionForgedBinding:
					projection.state.binding.Through = projection.state.binding.Through.Add(time.Hour)
					_, err = projection.CompareCaptured(t.Context(), f.captured)
					require.Error(t, err)
					return
				}
				_, err = ProjectIndependentHistory(t.Context(), f.source, request, f.bundle.Encryption)
			}
			require.Error(t, err)
		})
	}
}

func TestHistoryProjectionKVPreservesExpiredStateAndAtomicPreimages(t *testing.T) {
	root := privateKVDirectory(t)
	path := filepath.Join(root, historyProjectionEvidenceID)
	original := historyPayloadRecords{{Key: historyProjectionExpired, Value: []byte("original expired bytes"), ExpiresAtMillis: 1}, {Key: historyProjectionEmptyKey, Value: []byte{}}, {Key: historyProjectionPersistent, Value: []byte("original")}}
	receipt, err := SnapshotKV(t.Context(), original, path)
	require.NoError(t, err)
	view, err := OpenKVSnapshot(t.Context(), KVSnapshotPath(path), root, receipt)
	require.NoError(t, err)
	require.NoError(t, view.db.Close())
	view.db, err = openKVSnapshot(KVSnapshotPath(filepath.Join(root, view.name)), false)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, view.Close()) })
	prepared := preparedHistoryKV{digest: strings.Repeat("a", 64), mutations: []storage.CompareAndSwapMutation{{Key: historyProjectionEmptyKey, ExpectedValue: []byte{}, NewValue: nil}, {Key: historyProjectionCreatedEmpty, ExpectedValue: nil, NewValue: []byte{}}}}
	require.NoError(t, applyHistoryProjectionKV(t.Context(), view, prepared))
	_, err = view.ReadCaptured(t.Context(), historyProjectionEmptyKey, 128)
	require.ErrorIs(t, err, storage.ErrNotFound)
	empty, err := view.ReadCaptured(t.Context(), historyProjectionCreatedEmpty, 128)
	require.NoError(t, err)
	require.Empty(t, empty.Value)
	expired, err := view.ReadCaptured(t.Context(), historyProjectionExpired, 128)
	require.NoError(t, err)
	require.Equal(t, original[0], expired, "offline reconstruction must not drop or renew expired records")
	conflict := preparedHistoryKV{digest: strings.Repeat("b", 64), mutations: []storage.CompareAndSwapMutation{{Key: historyProjectionPersistent, ExpectedValue: []byte("original"), NewValue: []byte("would change")}, {Key: historyProjectionExpired, ExpectedValue: []byte("original expired bytes"), NewValue: []byte("unsafe replacement")}}}
	require.Error(t, applyHistoryProjectionKV(t.Context(), view, conflict))
	unchanged, err := view.ReadCaptured(t.Context(), historyProjectionPersistent, 128)
	require.NoError(t, err)
	require.Equal(t, "original", string(unchanged.Value), "a later conflict must roll back all private changes")
	conflict.mutations = []storage.CompareAndSwapMutation{{Key: historyProjectionCreatedEmpty, ExpectedValue: nil, NewValue: []byte("must refuse")}}
	require.Error(t, applyHistoryProjectionKV(t.Context(), view, conflict), "absence must not match a present empty value")
	retained, err := OpenKVSnapshot(t.Context(), KVSnapshotPath(path), root, receipt)
	require.NoError(t, err)
	require.NoError(t, retained.Close(), "private projection must not modify the original immutable snapshot")
}

func TestIndependentHistoryProjectionRechecksCapturedImagesAndRootReceipt(t *testing.T) {
	for _, name := range []string{historyProjectionChangedArchive, historyProjectionMissingArchive, historyProjectionChangedKV, historyProjectionMissingSQL, "changed-captured-manifest", "changed-original-manifest"} {
		t.Run(name, func(t *testing.T) {
			f := independentHistoryProjectionFixture(t)
			projection, err := ProjectIndependentHistory(t.Context(), f.source, f.request, f.bundle.Encryption)
			require.NoError(t, err)
			switch name {
			case historyProjectionChangedArchive:
				require.NoError(t, os.WriteFile(filepath.Join(f.captured.request.Directory, bundleBlobFile), []byte("changed after source inspection"), 0o600))
			case historyProjectionMissingArchive:
				require.NoError(t, os.Remove(filepath.Join(f.captured.request.Directory, bundleBlobFile)))
			case historyProjectionChangedKV:
				require.NoError(t, os.WriteFile(filepath.Join(f.captured.request.Directory, filepath.FromSlash(bundleKVFile)), []byte("changed after source inspection"), 0o600))
			case historyProjectionMissingSQL:
				require.NoError(t, os.Remove(filepath.Join(f.captured.request.Directory, filepath.FromSlash(bundleSQLFile))))
			case "changed-captured-manifest":
				require.NoError(t, os.WriteFile(filepath.Join(f.captured.request.Directory, bundleManifestFile), []byte("changed root receipt"), 0o600))
			case "changed-original-manifest":
				require.NoError(t, os.WriteFile(filepath.Join(f.source.request.Directory, bundleManifestFile), []byte("changed original root receipt"), 0o600))
				_, err = ProjectIndependentHistory(t.Context(), f.source, f.request, f.bundle.Encryption)
				require.Error(t, err, "retry must not trust a changed original root receipt")
				return
			}
			_, err = projection.CompareCaptured(t.Context(), f.captured)
			require.Error(t, err, "cached source inspection cannot replace current original-image verification")
		})
	}
}

func TestIndependentHistoryProjectionRetryRequiresCompleteOriginalArtifacts(t *testing.T) {
	for _, name := range []string{historyProjectionChangedKV, "missing-kv", "changed-sql", historyProjectionMissingSQL, historyProjectionChangedArchive, historyProjectionMissingArchive, "changed-selected-file", historyProjectionExtraFile} {
		t.Run(name, func(t *testing.T) {
			f := independentHistoryProjectionFixture(t)
			projection, err := ProjectIndependentHistory(t.Context(), f.source, f.request, f.bundle.Encryption)
			require.NoError(t, err)
			path := filepath.Join(f.source.request.Directory, filepath.FromSlash(bundleKVFile))
			switch name {
			case "changed-sql", historyProjectionMissingSQL:
				path = filepath.Join(f.source.request.Directory, filepath.FromSlash(bundleSQLFile))
			case historyProjectionChangedArchive, historyProjectionMissingArchive:
				path = filepath.Join(f.source.request.Directory, bundleBlobFile)
			case "changed-selected-file":
				path = filepath.Join(f.source.request.Directory, historyProjectionFileDirectory, "configuration", historyProjectionConfigurationFile)
			case historyProjectionExtraFile:
				path = filepath.Join(f.source.request.Directory, "unrecorded-artifact")
			}
			if strings.HasPrefix(name, "missing") {
				require.NoError(t, os.Remove(path))
			} else {
				require.NoError(t, os.WriteFile(path, []byte("changed original evidence after completion"), 0o600))
			}
			_, err = ProjectIndependentHistory(t.Context(), f.source, f.request, f.bundle.Encryption)
			require.Error(t, err, "completed outputs cannot waive original artifact retention")
			body, err := os.ReadFile(filepath.Join(f.request.Directory, historyProjectionRecordFile))
			require.NoError(t, err)
			require.Equal(t, projection.state.bytes, body, "refusal must preserve the original completed output record")
		})
	}
}
