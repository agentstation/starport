package credentials

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestCredentialPermissionReplayWithdrawsSharedGrants(t *testing.T) {
	for _, backend := range []string{"badger", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			source := permissionStore(t, backend)
			repo, err := Open(source)
			require.NoError(t, err)
			encryption, err := NewEncryptionService([]byte(strings.Repeat("k", 32)))
			require.NoError(t, err)
			ciphertext, err := encryption.EncryptCredential(`{"api_key":"private-fixture"}`)
			require.NoError(t, err)
			at := time.Now().UTC()
			initial, err := repo.Create(t.Context(), ProviderKey{Scope: SharedScope, Provider: "provider", Shared: []SharedCredential{{ID: "one", EncryptedCredential: ciphertext, Access: AccessGranted, Grants: []string{"tenant"}, CreatedAt: at, UpdatedAt: at}, {ID: "two", EncryptedCredential: ciphertext, Access: AccessOpen, CreatedAt: at, UpdatedAt: at}}, CreatedAt: at, UpdatedAt: at})
			require.NoError(t, err)
			before := capturePermissions(t, source)
			expected, err := CaptureRecoveryRecord(t.Context(), before, SharedScope, "provider", encryption)
			require.NoError(t, err)
			initial.Key.Shared = initial.Key.Shared[:1]
			initial.Key.Shared[0].Grants = nil
			later, err := repo.Update(t.Context(), initial.Key, initial.Revision)
			require.NoError(t, err)
			next, err := CaptureRecoveryRecord(t.Context(), capturePermissions(t, source), SharedScope, "provider", encryption)
			require.NoError(t, err)
			request := []RecoveryChange{{Scope: SharedScope, Provider: "provider", ExpectedSHA256: expected.SHA256(), Next: &next}}
			changes, err := PrepareRecoveryReplay(t.Context(), before, encryption, request)
			require.NoError(t, err)
			target, reconcile, claim := importedPermissions(t, backend, before)
			receipt, err := reconcile.ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), changes)
			require.NoError(t, err)
			retry, err := PrepareRecoveryReplay(t.Context(), before, encryption, request)
			require.NoError(t, err)
			require.Equal(t, changes, retry)
			repeated, err := reconcile.ReconcileImport(t.Context(), claim, 1, "", strings.Repeat("a", 64), retry)
			require.NoError(t, err)
			require.Equal(t, receipt, repeated)
			restored, err := Open(target)
			require.NoError(t, err)
			got, err := restored.Get(t.Context(), SharedScope, "provider")
			require.NoError(t, err)
			require.Equal(t, later, got)
			require.Len(t, got.Key.Shared, 1)
			require.False(t, got.Key.Shared[0].Usable("tenant"))
			require.Equal(t, ciphertext, got.Key.Shared[0].EncryptedCredential)
			closed := advancePermissions(before, changes)
			deletion, err := PrepareRecoveryReplay(t.Context(), closed, encryption, []RecoveryChange{{Scope: SharedScope, Provider: "provider", ExpectedSHA256: next.SHA256()}})
			require.NoError(t, err)
			_, err = reconcile.ReconcileImport(t.Context(), claim, 2, receipt, strings.Repeat("b", 64), deletion)
			require.NoError(t, err)
			_, err = restored.Get(t.Context(), SharedScope, "provider")
			require.ErrorIs(t, err, ErrNotFound)
			require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), target), storage.ErrImportRestricted)
		})
	}
}
func TestCredentialPermissionReplayRefusesUnsupportedPrivateFacts(t *testing.T) {
	source := permissionStore(t, "badger")
	repo, err := Open(source)
	require.NoError(t, err)
	encryption, err := NewEncryptionService([]byte(strings.Repeat("k", 32)))
	require.NoError(t, err)
	ciphertext, err := encryption.EncryptCredential(`{"api_key":"private-fixture"}`)
	require.NoError(t, err)
	_, err = repo.Create(t.Context(), ProviderKey{Scope: "tenant", Provider: "provider", EncryptedCredential: ciphertext})
	require.NoError(t, err)
	before := capturePermissions(t, source)
	record, err := CaptureRecoveryRecord(t.Context(), before, "tenant", "provider", encryption)
	require.NoError(t, err)
	require.Equal(t, "<private credentials recovery evidence>", fmt.Sprintf("%+v", record))
	require.NotContains(t, fmt.Sprintf("%#v", record), ciphertext)
	wrong, err := NewEncryptionService([]byte(strings.Repeat("z", 32)))
	require.NoError(t, err)
	_, err = PrepareRecoveryReplay(t.Context(), before, wrong, []RecoveryChange{{Scope: "tenant", Provider: "provider", ExpectedSHA256: record.SHA256(), Next: &record}})
	require.ErrorIs(t, err, ErrRecoveryCredential)
	require.NotContains(t, err.Error(), "private-fixture")
	raw, err := record.MarshalJSON()
	require.NoError(t, err)
	var roundtrip RecoveryRecord
	require.NoError(t, json.Unmarshal(raw, &roundtrip))
	require.Equal(t, record.SHA256(), roundtrip.SHA256())
	unknown := append(bytes.Clone(raw[:len(raw)-1]), []byte(`,"future_grants":[]}`)...)
	var rejected RecoveryRecord
	require.Error(t, json.Unmarshal(unknown, &rejected))
}

func TestCredentialPermissionReplayBoundsNativeStep(t *testing.T) {
	encryption, err := NewEncryptionService([]byte(strings.Repeat("k", 32)))
	require.NoError(t, err)
	ciphertext, err := encryption.EncryptCredential(`{"api_key":"private-fixture"}`)
	require.NoError(t, err)
	source := permissionStore(t, "badger")
	repo, err := Open(source)
	require.NoError(t, err)
	_, err = repo.Create(t.Context(), ProviderKey{Scope: "tenant", Provider: "provider", EncryptedCredential: ciphertext, Config: map[string]any{"large": strings.Repeat("x", storage.ImportReplayMaxBytes/2)}})
	require.NoError(t, err)
	before := capturePermissions(t, source)
	record, err := CaptureRecoveryRecord(t.Context(), before, "tenant", "provider", encryption)
	require.NoError(t, err)
	changes, err := PrepareRecoveryReplay(t.Context(), before, encryption, []RecoveryChange{{Scope: "tenant", Provider: "provider", ExpectedSHA256: record.SHA256(), Next: &record}})
	require.ErrorIs(t, err, storage.ErrValueTooLarge)
	require.Nil(t, changes)
	changes, err = PrepareRecoveryReplay(t.Context(), before, encryption, []RecoveryChange{{Scope: "tenant", Provider: "provider", ExpectedSHA256: record.SHA256()}})
	require.NoError(t, err)
	require.Len(t, changes, 1)
}
