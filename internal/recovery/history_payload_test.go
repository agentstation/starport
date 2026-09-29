package recovery

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func historyPayloadView(t *testing.T, source storage.RecordSource) *KVSnapshotView {
	t.Helper()
	root := privateKVDirectory(t)
	directory := filepath.Join(root, "capture")
	receipt, err := SnapshotKV(t.Context(), source, directory)
	require.NoError(t, err)
	view, err := OpenKVSnapshot(t.Context(), KVSnapshotPath(directory), root, receipt)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, view.Close()) })
	return view
}
func historyFixtureView(t *testing.T, f importedGraphFixture) *KVSnapshotView {
	t.Helper()
	return historyPayloadView(t, importedKVSource{f.sources.KV, f.request.KVClaim, f.request.KVPosition})
}
func historyPayloadJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return data
}
func TestHistoryPayloadNativePermissionAndFinalAuthority(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "badger"
		if shared {
			name = "valkey"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newImportedGraphFixture(t, shared)
			before := historyFixtureView(t, fixture)
			original, err := account.CaptureRecoveryRecord(t.Context(), before, "owner")
			require.NoError(t, err)
			var retained struct {
				SchemaVersion int             `json:"schema_version"`
				Revision      uint64          `json:"revision"`
				Account       account.Account `json:"account"`
			}
			require.NoError(t, json.Unmarshal(historyPayloadJSON(t, original), &retained))
			retained.Revision++
			retained.Account.Active = false
			var next account.RecoveryRecord
			require.NoError(t, next.UnmarshalJSON(historyPayloadJSON(t, retained)))
			payload := historyPayloadJSON(t, historyKVDomain{Version: 1, Accounts: []account.RecoveryChange{{ID: "owner", ExpectedSHA256: original.SHA256(), Next: &next}}})
			authority := revision.RecoveryAuthority{RecoveryID: "accepted-recovery", Epoch: "independent-permission-epoch"}
			prepare := func(data []byte) (preparedHistoryKV, error) {
				return prepareHistoryKV(t.Context(), "kv_domain", data, before, nil, time.Now(), fixture.encryption, authority)
			}
			prepared, err := prepare(payload)
			require.NoError(t, err)
			retry, err := prepare(payload)
			require.NoError(t, err)
			require.Equal(t, prepared.mutations, retry.mutations)
			require.Equal(t, prepared.digest, retry.digest)
			reconciler := fixture.target.KV.(storage.ImportReconciler)
			receipt, err := reconciler.ReconcileImport(t.Context(), fixture.request.KVClaim, 1, "", prepared.digest, prepared.mutations)
			require.NoError(t, err)
			fixture.request.KVPosition = storage.ImportReplayPosition{Sequence: 1, ReceiptSHA256: receipt}
			after := historyFixtureView(t, fixture)
			got, err := account.CaptureRecoveryRecord(t.Context(), after, "owner")
			require.NoError(t, err)
			require.Equal(t, next.SHA256(), got.SHA256())
			_, expected, err := revision.CaptureKVRecovery(t.Context(), after)
			require.NoError(t, err)
			transition := historyKVAuthorityPayload{Version: 1, ExpectedSHA256: expected}
			replacement, err := prepareHistoryKV(t.Context(), "kv_authorization_final", historyPayloadJSON(t, transition), after, nil, time.Now(), nil, authority)
			require.NoError(t, err)
			_, err = reconciler.ReconcileImport(t.Context(), fixture.request.KVClaim, 2, receipt, replacement.digest, replacement.mutations)
			require.NoError(t, err)
			again, err := reconciler.ReconcileImport(t.Context(), fixture.request.KVClaim, 1, "", prepared.digest, retry.mutations)
			require.NoError(t, err)
			require.Equal(t, receipt, again)
			other := authority
			other.Epoch = ""
			_, err = prepareHistoryKV(t.Context(), "kv_authorization_final", historyPayloadJSON(t, transition), after, nil, time.Now(), nil, other)
			require.Error(t, err)
			require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), fixture.kv), storage.ErrImportRestricted)
			for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%d", "%p"} {
				require.NotContains(t, fmt.Sprintf(verb, prepared), "owner")
			}
		})
	}
}
func TestHistoryPayloadRejectsUnsupportedOrImplicitMutations(t *testing.T) {
	before := historyPayloadView(t, historyPayloadRecords{})
	for name, data := range map[string]string{
		"empty":                      `{"version":1}`,
		"raw-cas":                    `{"version":1,"mutations":[{"key":"x","new_value":"eA=="}]}`,
		"future":                     `{"version":2}`,
		"duplicate":                  `{"version":1,"version":1}`,
		"missing-preimage":           `{"version":1,"accounts":[{"id":"owner","next":null}]}`,
		"missing-next":               `{"version":1,"accounts":[{"id":"owner","expected_sha256":""}]}`,
		"credential-implicit-delete": `{"version":1,"credentials":[{"scope":"shared","provider":"provider","expected_sha256":""}]}`,
		"file-implicit-delete":       `{"version":1,"files":[{"account":"owner","id":"file","expected_sha256":""}]}`,
		"nested-unknown":             `{"version":1,"accounts":[{"id":"owner","expected_sha256":"","next":null,"future":true}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			prepared, err := prepareHistoryKV(t.Context(), "kv_domain", []byte(data), before, nil, time.Now(), nil, revision.RecoveryAuthority{})
			require.Error(t, err)
			require.Empty(t, prepared.mutations)
		})
	}
	for _, kind := range []string{"raw", "sql_identity", "blob_publication", "window_stage", "window_finalize", "slot_stage", "slot_finalize", "storedbytes_stage", "storedbytes_finalize", "kv_authorization_final"} {
		t.Run(kind, func(t *testing.T) {
			_, err := prepareHistoryKV(t.Context(), kind, []byte(`{"version":1}`), before, nil, time.Now(), nil, revision.RecoveryAuthority{})
			require.Error(t, err)
		})
	}
	_, err := prepareHistoryKV(t.Context(), "kv_domain", []byte(strings.Repeat("x", typedHistoryPayloadMaxBytes+1)), before, nil, time.Now(), nil, revision.RecoveryAuthority{})
	require.Error(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = prepareHistoryKV(ctx, "kv_domain", []byte(`{"version":1}`), before, nil, time.Now(), nil, revision.RecoveryAuthority{})
	require.ErrorIs(t, err, context.Canceled)
}
func TestHistoryPayloadProposalBoundsAndDeletion(t *testing.T) {
	before := historyPayloadView(t, historyPayloadRecords{})
	proposal := newHistoryKVProposal(before)
	require.NoError(t, proposal.add([]storage.CompareAndSwapMutation{{Key: "deleted", ExpectedValue: []byte("private"), NewValue: nil}}))
	_, err := proposal.ReadCaptured(t.Context(), "deleted", 100)
	require.ErrorIs(t, err, storage.ErrNotFound)
	require.ErrorIs(t, proposal.add([]storage.CompareAndSwapMutation{{Key: "deleted"}}), storage.ErrInvalidMutation)
	require.ErrorIs(t, proposal.add([]storage.CompareAndSwapMutation{{Key: "storage:control", NewValue: []byte("unsafe")}}), storage.ErrInvalidMutation)
	require.ErrorIs(t, proposal.add([]storage.CompareAndSwapMutation{{Key: "oversized", NewValue: make([]byte, storage.ImportReplayMaxBytes)}}), storage.ErrValueTooLarge)
	proposal = newHistoryKVProposal(before)
	for index := range typedHistoryMaxMutations {
		require.NoError(t, proposal.add([]storage.CompareAndSwapMutation{{Key: fmt.Sprintf("key-%03d", index), NewValue: []byte("value")}}))
	}
	require.ErrorIs(t, proposal.add([]storage.CompareAndSwapMutation{{Key: "one-too-many", NewValue: []byte("value")}}), storage.ErrValueTooLarge)
	_, err = proposal.ReadCaptured(t.Context(), "key-001", 1)
	require.ErrorIs(t, err, storage.ErrValueTooLarge)
}

type historyPayloadRecords []storage.TransferRecord

func (r historyPayloadRecords) Enumerate(ctx context.Context, visit func(storage.TransferRecord) error) error {
	for _, record := range r {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(record); err != nil {
			return err
		}
	}
	return nil
}
