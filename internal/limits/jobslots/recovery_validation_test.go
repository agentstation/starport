package jobslots

import (
	"encoding/json/v2"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestRecoverySlotSchemas(t *testing.T) {
	claim := Claim{Version: recordVersion, CreatedAt: time.Now().UTC(), Account: "account", ID: "slot", JobID: "job", Kind: "video", Bound: 1}
	data, err := json.Marshal(claim)
	require.NoError(t, err)
	valid := storage.TransferRecord{Key: claimKey(claim.Account, claim.ID), Value: data}
	item, err := VerifyRecoveryRecord(valid)
	require.NoError(t, err)
	require.Equal(t, claim, *item.Claim)
	for _, mode := range []string{"expiring-claim", "expiring-counter", "expiring-history", "oversized", "wrong-key", "wrong-account", "old-version", "malformed", "invalid-kind", "negative-bound", "counter-scalar", "counter-version", "counter-negative", "history-value", "history-account", "unknown-suffix"} {
		t.Run(mode, func(t *testing.T) {
			record := valid
			switch mode {
			case "expiring-claim":
				record.ExpiresAtMillis = 1
			case "expiring-counter":
				record = storage.TransferRecord{Key: countKey("account"), Value: []byte(`{"version":3,"total":0}`), ExpiresAtMillis: 1}
			case "expiring-history":
				record = storage.TransferRecord{Key: historyKey("account"), Value: []byte("3"), ExpiresAtMillis: 1}
			case "oversized":
				record.Value = []byte(strings.Repeat(" ", maxRecordBytes+1))
			case "wrong-key":
				record.Key = claimKey("account", "other")
			case "wrong-account":
				record.Key = claimKey("other", "slot")
			case "old-version":
				changed := claim
				changed.Version--
				record.Value, err = json.Marshal(changed)
			case "malformed":
				record.Value = []byte("null")
			case "invalid-kind":
				changed := claim
				changed.Kind = "other"
				record.Value, err = json.Marshal(changed)
			case "negative-bound":
				changed := claim
				changed.Bound = -1
				record.Value, err = json.Marshal(changed)
			case "counter-scalar":
				record = storage.TransferRecord{Key: countKey("account"), Value: []byte("0")}
			case "counter-version":
				record = storage.TransferRecord{Key: countKey("account"), Value: []byte(`{"version":2,"total":0}`)}
			case "counter-negative":
				record = storage.TransferRecord{Key: countKey("account"), Value: []byte(`{"version":3,"total":-1}`)}
			case "history-value":
				record = storage.TransferRecord{Key: historyKey("account"), Value: []byte("2")}
			case "history-account":
				record = storage.TransferRecord{Key: claimPrefix + "!!!:history", Value: []byte("3")}
			case "unknown-suffix":
				record.Key = accountClaimPrefix("account") + "unknown"
			}
			require.NoError(t, err)
			_, err := VerifyRecoveryRecord(record)
			require.ErrorIs(t, err, ErrHistoryUnknown)
		})
	}
}

func TestRecoverySlotTotals(t *testing.T) {
	zero, one := int64(0), int64(1)
	require.NoError(t, VerifyRecoveryTotal(&zero, true, 0))
	require.NoError(t, VerifyRecoveryTotal(&one, true, 1))
	for _, fixture := range []struct {
		total   *int64
		history bool
		held    int64
	}{
		{nil, true, 0}, {&zero, false, 0}, {nil, false, 1}, {&zero, true, 1}, {&one, true, 0}, {&one, true, -1},
	} {
		require.ErrorIs(t, VerifyRecoveryTotal(fixture.total, fixture.history, fixture.held), ErrHistoryUnknown)
	}
}

func TestRecoverySlotAttachmentTransitions(t *testing.T) {
	for _, kind := range []string{"video", "batch"} {
		t.Run(kind, func(t *testing.T) {
			claim := Claim{Version: recordVersion, CreatedAt: time.Now().UTC(), Account: "account", ID: "slot", JobID: "job", Kind: kind, Bound: 1}
			work := RecoveryAttachment{Account: claim.Account, ClaimID: claim.ID, JobID: claim.JobID, Kind: kind}
			require.NoError(t, VerifyRecoveryAttachment(&claim, nil), "unattached capacity stays held")
			require.ErrorIs(t, VerifyRecoveryAttachment(&claim, &work), ErrHistoryUnknown, "lost attachment cannot become pending cleanup")
			claim.Attached = true
			require.NoError(t, VerifyRecoveryAttachment(&claim, &work))
			require.ErrorIs(t, VerifyRecoveryAttachment(&claim, nil), ErrHistoryUnknown)
			require.ErrorIs(t, VerifyRecoveryAttachment(nil, &work), ErrHistoryUnknown)
			for _, field := range []string{"account", "id", "job", "kind"} {
				wrong := work
				switch field {
				case "account":
					wrong.Account = "other"
				case "id":
					wrong.ClaimID = "other"
				case "job":
					wrong.JobID = "other"
				case "kind":
					wrong.Kind = "other"
				}
				require.ErrorIs(t, VerifyRecoveryAttachment(&claim, &wrong), ErrHistoryUnknown)
			}
			work.Released = true
			require.ErrorIs(t, VerifyRecoveryAttachment(&claim, &work), ErrHistoryUnknown)
			work.Finished = true
			require.ErrorIs(t, VerifyRecoveryAttachment(&claim, &work), ErrHistoryUnknown, "a release flag cannot replace a durable release")
			claim.Released = true
			require.NoError(t, VerifyRecoveryAttachment(&claim, &work))
			work.Released = false
			require.NoError(t, VerifyRecoveryAttachment(&claim, &work), "release committed before acknowledgement")
			work.Finished = false
			require.ErrorIs(t, VerifyRecoveryAttachment(&claim, &work), ErrHistoryUnknown)
			require.NoError(t, VerifyRecoveryAttachment(&claim, nil), "released history survives job retention")
		})
	}
}
