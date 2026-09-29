package recovery

import (
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/limits/jobslots"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/limits/storedbytes"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestHistoryPayloadNativeOwnerStages(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "badger"
		if shared {
			name = "valkey"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newImportedGraphFixture(t, shared)
			source, reader, _ := kvTransferStores(t, storage.StorageTypeBadger)
			slots, err := jobslots.Open(source)
			require.NoError(t, err)
			require.NoError(t, slots.Reserve(t.Context(), "recovered", "slot", "job", "video", 10))
			claim, err := slots.Get(t.Context(), "recovered", "slot")
			require.NoError(t, err)
			bytes, err := storedbytes.NewStorageMeter(source)
			require.NoError(t, err)
			require.NoError(t, bytes.InitializeEmpty(t.Context(), "recovered"))
			require.NoError(t, bytes.Reserve(t.Context(), "recovered", "file", 7, 100))
			independent := historyPayloadView(t, reader)
			slotState, err := jobslots.CaptureAccountReplayState(t.Context(), independent, "recovered")
			require.NoError(t, err)
			byteState, err := storedbytes.CaptureAccountReplayState(t.Context(), independent, "recovered")
			require.NoError(t, err)
			var byteClaims []storedbytes.RecoveryClaim
			require.NoError(t, independent.Enumerate(t.Context(), func(record storage.TransferRecord) error {
				if strings.HasPrefix(record.Key, storedbytes.StoredBytesPrefix) {
					item, err := storedbytes.VerifyRecoveryRecord(record)
					if err != nil {
						return err
					}
					if item.Claim != nil {
						byteClaims = append(byteClaims, *item.Claim)
					}
				}
				return nil
			}))
			before := historyFixtureView(t, fixture)
			var window *reservation.WindowState
			var attempts []reservation.Record
			require.NoError(t, before.Enumerate(t.Context(), func(record storage.TransferRecord) error {
				if !strings.HasPrefix(record.Key, reservation.StoragePrefix) {
					return nil
				}
				item, err := reservation.VerifyBackupRecord(t.Context(), before, record)
				if err != nil {
					return err
				}
				if item.Window != nil {
					window = item.Window
				}
				if item.Attempt != nil {
					attempts = append(attempts, *item.Attempt)
				}
				return nil
			}))
			require.NotNil(t, window)
			state, err := reservation.CaptureWindowReplayState(t.Context(), before, window.Meter, window.Window)
			require.NoError(t, err)
			steps := []struct {
				kind  string
				value any
			}{
				{"window_stage", historyWindowStage{Version: 1, State: state, Attempts: attempts}},
				{"window_finalize", historyWindowFinal{Version: 1, State: state}},
				{"slot_stage", historySlotStage{Version: 1, State: slotState, Claims: []jobslots.Claim{claim}}},
				{"slot_finalize", historySlotFinal{Version: 1, State: slotState}},
				{"storedbytes_stage", historyByteStage{Version: 1, State: byteState, Claims: byteClaims}},
				{"storedbytes_finalize", historyByteFinal{Version: 1, State: byteState}},
			}
			for _, step := range steps {
				view := historyFixtureView(t, fixture)
				prepared, err := prepareHistoryKV(t.Context(), step.kind, historyPayloadJSON(t, step.value), view, nil, time.Now(), nil, revision.RecoveryAuthority{})
				require.NoError(t, err, step.kind)
				receipt, err := fixture.target.KV.(storage.ImportReconciler).ReconcileImport(t.Context(), fixture.request.KVClaim, fixture.request.KVPosition.Sequence+1, fixture.request.KVPosition.ReceiptSHA256, prepared.digest, prepared.mutations)
				require.NoError(t, err, step.kind)
				fixture.request.KVPosition = storage.ImportReplayPosition{Sequence: fixture.request.KVPosition.Sequence + 1, ReceiptSHA256: receipt}
			}
			final := historyFixtureView(t, fixture)
			got, err := storedbytes.CaptureAccountReplayState(t.Context(), final, "recovered")
			require.NoError(t, err)
			require.Equal(t, byteState, got)
			gotSlots, err := jobslots.CaptureAccountReplayState(t.Context(), final, "recovered")
			require.NoError(t, err)
			require.Equal(t, slotState, gotSlots)
			require.ErrorIs(t, storage.CheckImportBarrier(t.Context(), fixture.kv), storage.ErrImportRestricted)
		})
	}
}
