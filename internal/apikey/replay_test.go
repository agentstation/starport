package apikey

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyPermissionReplayWithdrawsAndPreservesIndexes(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			source := permissionStore(t, backend)
			repo, err := Open(source)
			require.NoError(t, err)
			expires := time.Now().UTC().Add(time.Hour)
			created, err := repo.CreateInitial(t.Context(), APIKey{ID: "key", Name: "key", Hash: "private-hash", AccountID: "tenant", TeamID: "team", Scopes: []string{"chat:write", "models:read"}, Active: true, ExpiresAt: &expires, Limits: &limits.Limits{Spend: &limits.Budget{Limit: 100, Interval: limits.IntervalDay}}})
			require.NoError(t, err)
			before := capturePermissions(t, source)
			expected, err := CaptureRecoveryRecord(t.Context(), before, "key")
			require.NoError(t, err)
			created.APIKey.Active = false
			created.APIKey.Scopes = []string{"models:read"}
			created.APIKey.AllowedModels = []string{"model"}
			later, err := repo.Update(t.Context(), created.APIKey, created.Revision)
			require.NoError(t, err)
			current := capturePermissions(t, source)
			next, err := CaptureRecoveryRecord(t.Context(), current, "key")
			require.NoError(t, err)
			request := permissionRequest(t, before, current, RecoveryChange{ID: "key", ExpectedSHA256: expected.SHA256(), Next: &next})
			changes, err := PrepareRecoveryReplay(t.Context(), before, request)
			require.NoError(t, err)
			retry, err := PrepareRecoveryReplay(t.Context(), before, request)
			require.NoError(t, err)
			require.Equal(t, changes, retry)
			target, reconcile, claim := importedPermissions(t, backend, before)
			conflict := append([]storage.CompareAndSwapMutation(nil), changes...)
			conflict[0].ExpectedValue = []byte("stale preimage")
			_, err = reconcile.ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), conflict)
			require.ErrorIs(t, err, storage.ErrConflict)
			receipt, err := reconcile.ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), changes)
			require.NoError(t, err)
			again, err := reconcile.ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), retry)
			require.NoError(t, err)
			require.Equal(t, receipt, again)
			restored, err := Open(target)
			require.NoError(t, err)
			got, err := restored.GetByHash(t.Context(), "private-hash")
			require.NoError(t, err)
			require.Equal(t, later, got)
			require.False(t, got.APIKey.Active)
			require.Equal(t, expected.stored.APIKey.Limits.Spend.HistoryID, got.APIKey.Limits.Spend.HistoryID)
			closed := advancePermissions(before, changes)
			require.NoError(t, repo.Delete(t.Context(), "key", later.Revision))
			deletion := permissionRequest(t, closed, capturePermissions(t, source), RecoveryChange{ID: "key", ExpectedSHA256: next.SHA256()})
			changes, err = PrepareRecoveryReplay(t.Context(), closed, deletion)
			require.NoError(t, err)
			receipt, err = reconcile.ReconcileImport(t.Context(), claim, 2, receipt, strings.Repeat("b", 64), changes)
			require.NoError(t, err)
			closed = advancePermissions(closed, changes)
			_, err = restored.GetByHash(t.Context(), "private-hash")
			require.ErrorIs(t, err, ErrNotFound)
			indexes, _, err := CaptureRecoveryIndexes(t.Context(), closed)
			require.NoError(t, err)
			require.Equal(t, "key", indexes.InitialKeyID)
			require.Zero(t, indexes.Count)
			report, err := VerifyRecoverySnapshot(t.Context(), closed, func(context.Context, APIKey) (bool, bool, error) { return false, false, nil })
			require.NoError(t, err)
			require.EqualValues(t, 1, report.MissingInitialKeys)
			recreated, err := repo.Create(t.Context(), APIKey{ID: "key", Name: "replacement", Hash: "replacement-hash", Scopes: []string{"models:read"}, Active: false, Limits: &limits.Limits{Spend: &limits.Budget{Limit: 5, Interval: limits.IntervalDay}}})
			require.NoError(t, err)
			current = capturePermissions(t, source)
			replacement, err := CaptureRecoveryRecord(t.Context(), current, "key")
			require.NoError(t, err)
			request = permissionRequest(t, closed, current, RecoveryChange{ID: "key", Next: &replacement})
			changes, err = PrepareRecoveryReplay(t.Context(), closed, request)
			require.NoError(t, err)
			for _, mutation := range changes {
				require.NotContains(t, mutation.Key, ":history:")
				require.NotContains(t, mutation.Key, ":meter:")
			}
			_, err = reconcile.ReconcileImport(t.Context(), claim, 3, receipt, strings.Repeat("c", 64), changes)
			require.NoError(t, err)
			got, err = restored.GetByHash(t.Context(), "replacement-hash")
			require.NoError(t, err)
			require.Equal(t, recreated, got)
			_, err = reconcile.ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), retry)
			require.NoError(t, err)
			got, err = restored.GetByHash(t.Context(), "replacement-hash")
			require.NoError(t, err)
			require.Equal(t, recreated, got)
			_, err = restored.GetByHash(t.Context(), "private-hash")
			require.ErrorIs(t, err, ErrNotFound)
			require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), target), storage.ErrImportRestricted)
		})
	}
}

func TestAPIKeyPermissionReplayRequiresOrderedIdentityChanges(t *testing.T) {
	source := permissionStore(t, "badger")
	repo, err := Open(source)
	require.NoError(t, err)
	created, err := repo.Create(t.Context(), APIKey{ID: "key", Name: "key", Hash: "private-hash", Scopes: []string{"models:read"}, Active: true})
	require.NoError(t, err)
	_, err = repo.Update(t.Context(), created.APIKey, created.Revision)
	require.NoError(t, err)
	before := capturePermissions(t, source)
	record, err := CaptureRecoveryRecord(t.Context(), before, "key")
	require.NoError(t, err)
	for _, name := range []string{"hash", "creation time", "revision", "same revision different bytes", "identity"} {
		t.Run(name, func(t *testing.T) {
			later := record.stored
			later.Revision++
			switch name {
			case "hash":
				later.APIKey.Hash = "replacement-hash"
			case "creation time":
				later.APIKey.CreatedAt = later.APIKey.CreatedAt.Add(time.Second)
			case "revision":
				later.Revision = 1
			case "same revision different bytes":
				later.Revision = record.stored.Revision
				later.APIKey.Active = false
			case "identity":
				later.APIKey.ID = "other"
			}
			raw, err := json.Marshal(later)
			require.NoError(t, err)
			var next RecoveryRecord
			require.NoError(t, json.Unmarshal(raw, &next))
			request := permissionRequest(t, before, before, RecoveryChange{ID: "key", ExpectedSHA256: record.SHA256(), Next: &next})
			_, err = PrepareRecoveryReplay(t.Context(), before, request)
			require.Error(t, err)
		})
	}
}

func TestAPIKeyPermissionReplayRetainsPrivateBytes(t *testing.T) {
	source := permissionStore(t, "badger")
	repo, err := Open(source)
	require.NoError(t, err)
	_, err = repo.Create(t.Context(), APIKey{ID: "key", Name: "key", Hash: "private-hash", Scopes: []string{"models:read"}, Active: true})
	require.NoError(t, err)
	before := capturePermissions(t, source)
	record, err := CaptureRecoveryRecord(t.Context(), before, "key")
	require.NoError(t, err)
	later := record.stored
	later.Revision++
	later.APIKey.Metadata = map[string]any{"exact_integer": uint64(9007199254740993)}
	raw, err := json.Marshal(later)
	require.NoError(t, err)
	var next RecoveryRecord
	require.NoError(t, json.Unmarshal(raw, &next))
	encoded, err := json.Marshal(next)
	require.NoError(t, err)
	var roundtrip RecoveryRecord
	require.NoError(t, json.Unmarshal(encoded, &roundtrip))
	require.Equal(t, next.SHA256(), roundtrip.SHA256())
	changes, err := PrepareRecoveryReplay(t.Context(), before, permissionRequest(t, before, before, RecoveryChange{ID: "key", ExpectedSHA256: record.SHA256(), Next: &roundtrip}))
	require.NoError(t, err)
	index := slices.IndexFunc(changes, func(change storage.CompareAndSwapMutation) bool { return change.Key == apiKeyStorageKey("key") })
	require.NotEqual(t, -1, index)
	require.Equal(t, raw, changes[index].NewValue)
	require.Contains(t, string(changes[index].NewValue), "9007199254740993")
}

func permissionRequest(t *testing.T, before, after permissionSnapshot, change RecoveryChange) RecoveryReplay {
	t.Helper()
	_, digest, err := CaptureRecoveryIndexes(t.Context(), before)
	require.NoError(t, err)
	indexes, _, err := CaptureRecoveryIndexes(t.Context(), after)
	require.NoError(t, err)
	return RecoveryReplay{ExpectedIndexesSHA256: digest, Indexes: indexes, Changes: []RecoveryChange{change}}
}

func TestAPIKeyPermissionReplayRefusesUnsupportedEvidence(t *testing.T) {
	source := permissionStore(t, "badger")
	repo, err := Open(source)
	require.NoError(t, err)
	_, err = repo.CreateInitial(t.Context(), APIKey{ID: "key", Name: "key", Hash: "private-hash", Scopes: []string{"models:read"}, Active: true})
	require.NoError(t, err)
	before := capturePermissions(t, source)
	record, err := CaptureRecoveryRecord(t.Context(), before, "key")
	require.NoError(t, err)
	require.Equal(t, "<private apikey recovery evidence>", fmt.Sprintf("%#v", record))
	raw, err := record.MarshalJSON()
	require.NoError(t, err)
	var decoded RecoveryRecord
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, record.SHA256(), decoded.SHA256())
	unknown := append(bytes.Clone(raw[:len(raw)-1]), []byte(`,"future_permission":true}`)...)
	require.Error(t, json.Unmarshal(unknown, &decoded))
	for _, name := range []string{"unknown key", "foreign hash", "missing hash", "unknown index", "expiring hash", "missing collection", "wrong metadata digest", "wrong record digest", "initial removal", "initial replacement", "wrong count", "revision regression", "duplicate change"} {
		t.Run(name, func(t *testing.T) {
			current := maps.Clone(before)
			request := permissionRequest(t, current, current, RecoveryChange{ID: "key", ExpectedSHA256: record.SHA256(), Next: &record})
			switch name {
			case "unknown key":
				current[apiKeyStorageKey("key")] = storage.TransferRecord{Key: apiKeyStorageKey("key"), Value: unknown}
			case "foreign hash":
				current[hashStorageKey("private-hash")] = storage.TransferRecord{Key: hashStorageKey("private-hash"), Value: []byte(`{"schema_version":1,"identity_id":"other"}`)}
			case "missing hash":
				delete(current, hashStorageKey("private-hash"))
			case "unknown index":
				current[hashStorageKey("private-hash")] = storage.TransferRecord{Key: hashStorageKey("private-hash"), Value: []byte(`{"schema_version":1,"identity_id":"key","future":true}`)}
			case "expiring hash":
				value := current[hashStorageKey("private-hash")]
				value.ExpiresAtMillis = time.Now().Add(time.Hour).UnixMilli()
				current[value.Key] = value
			case "missing collection":
				delete(current, collectionKey)
				delete(current, initialKey)
				_, request.ExpectedIndexesSHA256, err = CaptureRecoveryIndexes(t.Context(), current)
				require.NoError(t, err)
			case "wrong metadata digest":
				request.ExpectedIndexesSHA256 = strings.Repeat("0", 64)
			case "wrong record digest":
				request.Changes[0].ExpectedSHA256 = strings.Repeat("0", 64)
			case "initial removal":
				request.Indexes.InitialKeyID = ""
			case "initial replacement":
				request.Indexes.InitialKeyID = "other"
			case "wrong count":
				request.Indexes.Count++
			case "revision regression":
				request.Indexes.Revision = 0
			case "duplicate change":
				request.Changes = append(request.Changes, request.Changes[0])
			}
			_, err = PrepareRecoveryReplay(t.Context(), current, request)
			require.Error(t, err)
		})
	}
}
