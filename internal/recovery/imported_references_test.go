package recovery

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/jobslots"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type importedGraphFixture struct {
	sources    ImportedReferenceSources
	request    ImportedReferenceRequest
	encryption *credentials.EncryptionService
	target     BundleTargets
	kv         storage.KVStore
}

func newImportedGraphFixture(t *testing.T, shared bool) importedGraphFixture {
	t.Helper()
	source, capture, directory := backupBundleRecipe(t, shared)
	// Add real owner state to exercise census and untouched policy validation.
	kind := storage.StorageTypeBadger
	if shared {
		kind = storage.StorageTypeValkey
	}
	kv, transfer, _ := kvTransferStores(t, kind)
	source.KV = transfer
	accounts, err := account.Open(kv)
	require.NoError(t, err)
	_, err = accounts.Create(t.Context(), account.Account{ID: "owner", Name: "Owner", Active: true, Limits: &limits.Limits{Spend: &limits.Budget{Limit: 1000, Interval: limits.IntervalDay}}})
	require.NoError(t, err)
	keys, err := apikey.Open(kv)
	require.NoError(t, err)
	_, err = keys.CreateInitial(t.Context(), apikey.APIKey{ID: "key", Name: "Key", Hash: "retained-hash", AccountID: "owner", Active: true, Scopes: []string{"chat:write"}, CreatedAt: time.Now().UTC()})
	require.NoError(t, err)
	people, err := identity.Open(source.SQL)
	require.NoError(t, err)
	_, err = people.Users.Create(t.Context(), identity.User{ID: "user", Subject: "retained-subject"})
	require.NoError(t, err)
	_, err = people.Teams.Create(t.Context(), identity.Team{ID: "team", Name: "Team"})
	require.NoError(t, err)
	var timed storage.TimeBoundStore
	if provider, ok := kv.(storage.IncarnationProvider); ok {
		incarnation, err := provider.ObserveIncarnation(t.Context())
		require.NoError(t, err)
		bound, err := provider.BindIncarnation(t.Context(), incarnation)
		require.NoError(t, err)
		timed = bound.(storage.TimeBoundStore)
	} else {
		timed = kv.(storage.TimeBoundStore)
	}
	budgets, err := reservation.Open(timed)
	require.NoError(t, err)
	meter := reservation.Meter{Scope: limits.ScopeAccount, Holder: "execution-owner", Dimension: limits.DimensionSpend, Interval: limits.IntervalDay}
	require.NoError(t, budgets.EstablishWindow(t.Context(), meter, time.Now(), 17, reservation.History{ID: "retained-history", Proof: "fixture-independent-history"}))
	attempt := reservation.Attempt{ID: "uncertain-attempt", RequestID: "request", AccountID: "execution-owner", KeyID: "key", OfferingID: "provider/model", CatalogGeneration: "generation", Operation: "chat", Rules: []reservation.Rule{{Meter: meter, Limit: 1000, PolicyRevision: "policy", HistoryID: "retained-history"}}, Valuation: reservation.Valuation{Version: reservation.ArithmeticVersion, Components: []reservation.Component{{Unit: "output", Price: reservation.Price{USD: "0.000000001", PerUnits: 1}}}}, Bound: reservation.Quantities{"output": 100}}
	_, err = budgets.Reserve(t.Context(), attempt)
	require.NoError(t, err)
	require.NoError(t, budgets.Begin(t.Context(), attempt.ID))
	require.NoError(t, budgets.MarkUncertain(t.Context(), attempt.ID, "lost-response"))
	manifest, err := BackupBundle(t.Context(), directory, source, capture)
	require.NoError(t, err)
	digest, err := manifest.Digest()
	require.NoError(t, err)
	target, importedKV, _ := bundleRestoreTargets(t, shared)
	scratch := privateKVDirectory(t)
	prepared, err := PrepareBundle(t.Context(), target, VerifyRequest{Directory: directory, ManifestSHA256: digest, ScratchDirectory: scratch}, "graph-import", source.Encryption)
	require.NoError(t, err)
	body, err := json.Marshal(struct {
		Version             int
		Operation, Manifest string
		FencingEvidence     string `json:",omitempty"`
	}{1, "graph-import", digest, ""})
	require.NoError(t, err)
	componentSum := sha256.Sum256(body)
	operation := hex.EncodeToString(componentSum[:])
	body, err = json.Marshal(struct {
		Version   string
		Operation string
		Manifest  string
		Boundary  Record
	}{"sql-restore-closed-v1", operation, digest, manifest.Request.Boundary})
	require.NoError(t, err)
	restrictionSum := sha256.Sum256(body)
	claim, err := json.Marshal(kvImportClaim{Version: 1, OperationID: operation, Snapshot: manifest.KV})
	require.NoError(t, err)
	return importedGraphFixture{
		sources:    ImportedReferenceSources{KV: target.KV.(storage.ImportInspector), SQL: target.SQL, Blobs: target.Blobs.(blob.ImportInspector)},
		request:    ImportedReferenceRequest{Boundary: prepared.Boundary, CapturedAt: manifest.StartedAt, KVClaim: claim, SQLOriginal: manifest.SQL, SQLIdentity: sqlstore.RelationalImportIdentity{OperationID: operation, RestrictionID: hex.EncodeToString(restrictionSum[:])}, BlobOperation: operation, BlobOriginal: manifest.Blobs},
		encryption: source.Encryption, target: target, kv: importedKV,
	}
}

func (f importedGraphFixture) inspect(t *testing.T, inspectors ...CapturedKVInspector) (ImportedReferences, error) {
	t.Helper()
	root := privateKVDirectory(t)
	return InspectImportedReferences(t.Context(), f.sources, f.request, filepath.Join(root, "graph"), root, f.encryption, inspectors...)
}

type repeatedImportedRecords struct {
	storage.ImportInspector
	conflict bool
}

func (s repeatedImportedRecords) InspectImport(ctx context.Context, claim []byte, position storage.ImportReplayPosition, visit func(storage.TransferRecord) error) error {
	return s.ImportInspector.InspectImport(ctx, claim, position, func(record storage.TransferRecord) error {
		if err := visit(record); err != nil {
			return err
		}
		if s.conflict {
			record.Value = append(append([]byte(nil), record.Value...), 'x')
		}
		return visit(record)
	})
}

func TestImportedReferencesNativeClosedGraph(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "badger-sqlite-filesystem"
		if shared {
			name = "valkey-postgres-objectstore"
		}
		t.Run(name, func(t *testing.T) {
			f := newImportedGraphFixture(t, shared)
			f.sources.KV = repeatedImportedRecords{ImportInspector: f.sources.KV}
			result, err := f.inspect(t)
			require.NoError(t, err)
			require.Len(t, result.RequestSHA256, 64)
			require.EqualValues(t, 1, result.References.AccountRecords)
			require.EqualValues(t, 1, result.References.GatewayKeys.Keys)
			require.EqualValues(t, 1, result.References.Identity.Users)
			require.EqualValues(t, 1, result.References.Identity.Teams)
			require.EqualValues(t, 1, result.References.HeldReservations)
			require.EqualValues(t, 1, result.References.BudgetWindows)
			require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), f.kv), storage.ErrImportRestricted)
			require.ErrorIs(t, f.target.SQL.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
			require.NoError(t, f.sources.Blobs.CheckImport(t.Context(), f.request.BlobOperation, f.request.BlobOriginal))
			again, err := f.inspect(t)
			require.NoError(t, err)
			require.Equal(t, result.RequestSHA256, again.RequestSHA256)
			require.Equal(t, result.References, again.References)
			require.Equal(t, result.KV, again.KV)
			// Advancing native replay changes the required position, even with valid graph data.
			receipt, err := f.target.KV.(storage.ImportReconciler).ReconcileImport(t.Context(), f.request.KVClaim, 1, "", strings.Repeat("a", 64), []storage.CompareAndSwapMutation{{Key: "test:later", NewValue: []byte("retained")}})
			require.NoError(t, err)
			rejected, err := f.inspect(t)
			require.Error(t, err)
			require.Empty(t, rejected.RequestSHA256)
			f.request.KVPosition = storage.ImportReplayPosition{Sequence: 1, ReceiptSHA256: receipt}
			next, err := f.inspect(t)
			require.NoError(t, err)
			require.NotEqual(t, result.RequestSHA256, next.RequestSHA256)
			sqlReceipt, err := f.target.SQL.ReplayRelationalImport(t.Context(), f.request.SQLOriginal, f.request.SQLIdentity, sqlstore.RelationalReplayStep{Sequence: 1, EvidenceSHA256: strings.Repeat("b", 64), TransitionSHA256: strings.Repeat("c", 64)}, func(context.Context, *sql.Conn) error { return nil })
			require.NoError(t, err)
			_, err = f.inspect(t)
			require.Error(t, err)
			f.request.SQLPosition = sqlstore.RelationalReplayPosition{Sequence: 1, ReceiptSHA256: sqlReceipt}
			final, err := f.inspect(t)
			require.NoError(t, err)
			require.NotEqual(t, next.RequestSHA256, final.RequestSHA256)
		})
	}
}

func TestImportedReferencesRefuseChangedOwnershipAndGraph(t *testing.T) {
	for _, mode := range []string{"claim", "boundary", "sql-identity", "blob-operation", "duplicates-conflict", "slot-stage", "window-stage", "unknown-account", "key-unknown", "sql-unknown", "nil-inspector", "late-kv-replay", "late-sql-replay", "unknown-budget-history", "late-sql-boundary"} {
		t.Run(mode, func(t *testing.T) {
			f := newImportedGraphFixture(t, false)
			var inspectors []CapturedKVInspector
			switch mode {
			case "claim":
				f.request.KVClaim = []byte("foreign")
			case "boundary":
				f.request.Boundary.Epoch++
			case "sql-identity":
				f.request.SQLIdentity.RestrictionID = "foreign"
			case "blob-operation":
				f.request.BlobOperation = "foreign"
			case "duplicates-conflict":
				f.sources.KV = repeatedImportedRecords{ImportInspector: f.sources.KV, conflict: true}
			case "unknown-budget-history":
				keys, err := f.kv.ScanWithPrefix(t.Context(), "budget:v1:history:", 0)
				require.NoError(t, err)
				require.Len(t, keys, 2)
				for _, key := range keys {
					value, err := f.kv.Get(t.Context(), key)
					require.NoError(t, err)
					if !strings.Contains(string(value), "retained-history") {
						require.NoError(t, f.kv.Delete(t.Context(), key))
					}
				}
			case "slot-stage":
				require.NoError(t, f.kv.Set(t.Context(), jobslots.ReplayStoragePrefix+"unfinished", []byte("{}")))
			case "window-stage":
				require.NoError(t, f.kv.Set(t.Context(), reservation.WindowReplayPrefix+"unfinished", []byte("{}")))
			case "unknown-account", "key-unknown":
				prefix := account.StoragePrefix
				if mode == "key-unknown" {
					prefix = "identity:v1:key:"
				}
				keys, err := f.kv.ScanWithPrefix(t.Context(), prefix, 0)
				require.NoError(t, err)
				require.Len(t, keys, 1)
				value, err := f.kv.Get(t.Context(), keys[0])
				require.NoError(t, err)
				value = append(value[:len(value)-1], []byte(`,"unknown_permission":true}`)...)
				require.NoError(t, f.kv.Set(t.Context(), keys[0], value))
			case "sql-unknown":
				var value string
				require.NoError(t, f.target.SQL.QueryRowContext(t.Context(), `SELECT record FROM users WHERE id='user'`).Scan(&value))
				_, err := f.target.SQL.ExecContext(t.Context(), `UPDATE users SET record=? WHERE id='user'`, strings.TrimSuffix(value, "}")+`,"unknown_permission":true}`)
				require.NoError(t, err)
			case "nil-inspector":
				inspectors = []CapturedKVInspector{nil}
			case "late-kv-replay":
				inspectors = []CapturedKVInspector{func(ctx context.Context, _ *KVSnapshotView, _ Record) error {
					_, err := f.target.KV.(storage.ImportReconciler).ReconcileImport(ctx, f.request.KVClaim, 1, "", strings.Repeat("d", 64), []storage.CompareAndSwapMutation{{Key: "test:later", NewValue: []byte("retained")}})
					return err
				}}
			case "late-sql-boundary":
				inspectors = []CapturedKVInspector{func(ctx context.Context, _ *KVSnapshotView, _ Record) error {
					witness, err := New(f.target.SQL)
					if err != nil {
						return err
					}
					_, err = witness.Close(ctx, f.request.Boundary)
					return err
				}}
			case "late-sql-replay":
				inspectors = []CapturedKVInspector{func(ctx context.Context, _ *KVSnapshotView, _ Record) error {
					_, err := f.target.SQL.ReplayRelationalImport(ctx, f.request.SQLOriginal, f.request.SQLIdentity, sqlstore.RelationalReplayStep{Sequence: 1, EvidenceSHA256: strings.Repeat("b", 64), TransitionSHA256: strings.Repeat("c", 64)}, func(context.Context, *sql.Conn) error { return nil })
					return err
				}}
			}
			result, err := f.inspect(t, inspectors...)
			require.Error(t, err)
			require.Equal(t, ImportedReferences{}, result)
			require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), f.kv), storage.ErrImportRestricted)
			require.ErrorIs(t, f.target.SQL.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
		})
	}
}

func TestImportedReferencesCanceled(t *testing.T) {
	f := newImportedGraphFixture(t, false)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	root := privateKVDirectory(t)
	result, err := InspectImportedReferences(ctx, f.sources, f.request, filepath.Join(root, "graph"), root, f.encryption)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, result.RequestSHA256)
	require.NoDirExists(t, filepath.Join(root, "graph"))
}

func TestImportedReferencesRequireExactBlobReplayPosition(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "local"
		if shared {
			name = "shared"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newImportedGraphFixture(t, shared)
			initial, err := fixture.inspect(t)
			require.NoError(t, err)
			payload := "independently retained bytes"
			sum := sha256.Sum256([]byte(payload))
			step := blob.ImportPublicationStep{Sequence: 1, EvidenceSHA256: strings.Repeat("a", 64), Key: "later-independent-upload", Expected: blob.PublicationState{Kind: "absent"}, Next: blob.PublicationState{Kind: "live", Size: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}}
			replay := fixture.target.Blobs.(blob.ImportPublicationReplayer)
			receipt, err := replay.ReplayPublication(t.Context(), fixture.request.BlobOperation, fixture.request.BlobOriginal, step, strings.NewReader(payload), privateKVDirectory(t))
			require.NoError(t, err)
			refused, err := fixture.inspect(t)
			require.Error(t, err)
			require.Empty(t, refused.RequestSHA256)
			fixture.request.BlobPosition = blob.ImportReplayPosition{Sequence: 1, ReceiptSHA256: receipt}
			current, err := fixture.inspect(t)
			require.NoError(t, err)
			require.NotEqual(t, initial.RequestSHA256, current.RequestSHA256)
			step.Sequence = 2
			step.PreviousSHA256 = receipt
			step.Expected = step.Next
			step.Next = blob.PublicationState{Kind: "retired"}
			_, err = replay.ReplayPublication(t.Context(), fixture.request.BlobOperation, fixture.request.BlobOriginal, step, nil, privateKVDirectory(t))
			require.NoError(t, err)
			refused, err = fixture.inspect(t)
			require.Error(t, err)
			require.Empty(t, refused.RequestSHA256)
		})
	}
}
