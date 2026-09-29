package account

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestAccountPermissionReplayWithdrawsAndPreservesHistory(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			source := permissionStore(t, backend)
			repository, err := Open(source)
			require.NoError(t, err)
			created, err := repository.Create(t.Context(), Account{ID: "tenant", Name: "Tenant", Active: true, Limits: &limits.Limits{Spend: &limits.Budget{Limit: 1000, Interval: limits.IntervalDay}}})
			require.NoError(t, err)
			before := capturePermissions(t, source)
			expected, err := CaptureRecoveryRecord(t.Context(), before, "tenant")
			require.NoError(t, err)
			created.Account.Active = false
			created.Account.BYOKPolicy = &BYOKPolicy{Mode: BYOKNone}
			created.Account.CredentialStrategy = StrategyBYOKOnly
			created.Account.Limits.Spend.Limit = 12
			later, err := repository.Update(t.Context(), created.Account, created.Revision)
			require.NoError(t, err)
			observed := capturePermissions(t, source)
			next, err := CaptureRecoveryRecord(t.Context(), observed, "tenant")
			require.NoError(t, err)
			request := []RecoveryChange{{ID: "tenant", ExpectedSHA256: expected.SHA256(), Next: &next}}
			changes, err := PrepareRecoveryReplay(t.Context(), before, request)
			require.NoError(t, err)
			retry, err := PrepareRecoveryReplay(t.Context(), before, request)
			require.NoError(t, err)
			require.Equal(t, changes, retry)
			target, reconcile, claim := importedPermissions(t, backend, before)
			receipt, err := reconcile.ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), changes)
			require.NoError(t, err)
			again, err := reconcile.ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), retry)
			require.NoError(t, err)
			require.Equal(t, receipt, again)
			restored, err := Open(target)
			require.NoError(t, err)
			got, err := restored.GetByID(t.Context(), "tenant")
			require.NoError(t, err)
			require.Equal(t, later, got)
			require.False(t, got.Account.Active)
			require.False(t, got.Account.AllowsBYOK("provider"))
			require.Equal(t, expected.stored.Account.Limits.Spend.HistoryID, got.Account.Limits.Spend.HistoryID)
			closed := advancePermissions(before, changes)
			deletion, err := PrepareRecoveryReplay(t.Context(), closed, []RecoveryChange{{ID: "tenant", ExpectedSHA256: next.SHA256()}})
			require.NoError(t, err)
			receipt, err = reconcile.ReconcileImport(t.Context(), claim, 2, receipt, strings.Repeat("b", 64), deletion)
			require.NoError(t, err)
			closed = advancePermissions(closed, deletion)
			require.NoError(t, repository.Delete(t.Context(), "tenant", later.Revision))
			recreated, err := repository.Create(t.Context(), Account{ID: "tenant", Name: "Replacement", Active: false, Limits: &limits.Limits{Spend: &limits.Budget{Limit: 7, Interval: limits.IntervalDay}}})
			require.NoError(t, err)
			replacement, err := CaptureRecoveryRecord(t.Context(), capturePermissions(t, source), "tenant")
			require.NoError(t, err)
			replacementChanges, err := PrepareRecoveryReplay(t.Context(), closed, []RecoveryChange{{ID: "tenant", Next: &replacement}})
			require.NoError(t, err)
			_, err = reconcile.ReconcileImport(t.Context(), claim, 3, receipt, strings.Repeat("c", 64), replacementChanges)
			require.NoError(t, err)
			got, err = restored.GetByID(t.Context(), "tenant")
			require.NoError(t, err)
			require.Equal(t, recreated, got)
			for _, mutation := range replacementChanges {
				require.NotContains(t, mutation.Key, ":history:")
				require.NotContains(t, mutation.Key, ":meter:")
			}
			marker, err := reservation.FreshHolderIdentity(limits.ScopeAccount, "tenant")
			require.NoError(t, err)
			value, err := target.Get(t.Context(), marker.Key)
			require.NoError(t, err)
			require.Equal(t, marker.NewValue, value)
			require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), target), storage.ErrImportRestricted)
		})
	}
}

func TestAccountPermissionReplayRefusesUnsupportedOrStaleEvidence(t *testing.T) {
	source := permissionStore(t, "badger")
	repo, err := Open(source)
	require.NoError(t, err)
	_, err = repo.EnsureDefault(t.Context())
	require.NoError(t, err)
	before := capturePermissions(t, source)
	record, err := CaptureRecoveryRecord(t.Context(), before, DefaultID)
	require.NoError(t, err)
	require.Equal(t, "<private account recovery evidence>", fmt.Sprintf("%#v", record))
	_, err = PrepareRecoveryReplay(t.Context(), before, []RecoveryChange{{ID: DefaultID, ExpectedSHA256: record.SHA256()}})
	require.ErrorIs(t, err, ErrDefaultImmutable)
	_, err = PrepareRecoveryReplay(t.Context(), before, []RecoveryChange{{ID: DefaultID, ExpectedSHA256: strings.Repeat("0", 64), Next: &record}})
	require.Error(t, err)
	raw, err := record.MarshalJSON()
	require.NoError(t, err)
	var roundtrip RecoveryRecord
	require.NoError(t, json.Unmarshal(raw, &roundtrip))
	require.Equal(t, record.SHA256(), roundtrip.SHA256())
	unsupported := append(bytes.Clone(raw[:len(raw)-1]), []byte(`,"future_permission":true}`)...)
	var next RecoveryRecord
	require.Error(t, json.Unmarshal(unsupported, &next))
	before[accountStorageKey(DefaultID)] = storage.TransferRecord{Key: accountStorageKey(DefaultID), Value: unsupported}
	_, err = PrepareRecoveryReplay(t.Context(), before, []RecoveryChange{{ID: DefaultID, ExpectedSHA256: record.SHA256(), Next: &record}})
	require.Error(t, err)
}
