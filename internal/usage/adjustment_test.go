package usage

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestUsageCorrectionPreservesRequestCount(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		repository, err := Open(store, Options{})
		require.NoError(t, err)
		original := testRecord("key", "video", time.Now().UTC())
		original.Operation = OperationVideos
		original.AccountID, original.TeamID = "account", "team"
		require.NoError(t, repository.Put(t.Context(), original))
		corrected := original
		corrected.Cost = &Cost{NanoUSD: 375000000, Currency: "USD"}
		corrected.BillingDisposition = "administrator_usage"
		require.NoError(t, repository.(AdjustmentWriter).Adjust(t.Context(), Adjustment{ID: "correction-1", Original: original, RecordedAt: original.Timestamp.Add(time.Second), Cost: *corrected.Cost, Tokens: original.Tokens.Total, BillingDisposition: corrected.BillingDisposition}))
		totals, err := repository.Totals(t.Context(), AccountScope("account"), IntervalDay, original.Timestamp)
		require.NoError(t, err)
		require.Equal(t, int64(1), totals.Requests)
		require.Equal(t, int64(375000000), totals.SpendNanoUSD)
	})
}

func adjustmentFixture(original Record, id, previous string, cost, tokens int64) Adjustment {
	return Adjustment{ID: id, PreviousID: previous, Original: original, RecordedAt: original.Timestamp.Add(time.Second), Cost: Cost{NanoUSD: cost, Currency: "USD"}, Tokens: tokens, BillingDisposition: "administrator_usage"}
}

func TestUsageAdjustmentsPreserveEvidenceAndWindows(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		repository, err := Open(store, Options{})
		require.NoError(t, err)
		original := testRecord("key", "video", time.Now().UTC().Add(-25*time.Hour))
		original.Operation, original.AccountID, original.TeamID = OperationVideos, "account", "team"
		require.NoError(t, repository.Put(t.Context(), original))
		key := recordKey(original.KeyID, original.Timestamp, original.RequestID)
		raw, err := store.Get(t.Context(), key)
		require.NoError(t, err)
		another := original
		another.RequestID = "unrelated"
		require.NoError(t, repository.Put(t.Context(), another))
		adjustment := adjustmentFixture(original, "decision-1", "", 900, 17)
		writer := repository.(AdjustmentWriter)
		require.NoError(t, writer.Adjust(t.Context(), adjustment))
		require.NoError(t, writer.Adjust(t.Context(), adjustment))
		for _, scope := range []Scope{GatewayScope(), KeyScope("key"), AccountScope("account"), TeamScope("team")} {
			for _, interval := range []string{IntervalDay, IntervalWeek, IntervalMonth} {
				totals, err := repository.Totals(t.Context(), scope, interval, original.Timestamp)
				require.NoError(t, err)
				require.Equal(t, Totals{Requests: 2, Tokens: original.Tokens.Total + 17, SpendNanoUSD: original.Cost.NanoUSD + 900}, totals)
			}
		}
		today, err := repository.Totals(t.Context(), GatewayScope(), IntervalDay, time.Now())
		require.NoError(t, err)
		require.Equal(t, Totals{}, today)
		kept, err := store.Get(t.Context(), key)
		require.NoError(t, err)
		require.Equal(t, raw, kept)
		require.NoError(t, repository.Put(t.Context(), original), "replay of the initial report cannot undo its correction")
		page, err := repository.List(t.Context(), Query{AccountID: "account", RequestID: original.RequestID})
		require.NoError(t, err)
		require.Len(t, page.Records, 1)
		projected := page.Records[0]
		require.Equal(t, adjustment.Cost, *projected.Cost)
		require.Equal(t, original.Cost, projected.BillingAdjustment.OriginalCost)
		require.Equal(t, original.Tokens, projected.BillingAdjustment.OriginalTokens)
		require.Equal(t, adjustment.Tokens, projected.Tokens.Total)
		require.ErrorIs(t, repository.Put(t.Context(), projected), ErrInvalidRecord)
		second := adjustmentFixture(original, "decision-2", adjustment.ID, 0, 0)
		second.BillingDisposition = "administrator_no_charge"
		require.NoError(t, writer.Adjust(t.Context(), second))
		require.NoError(t, writer.Adjust(t.Context(), adjustment), "an older retry must return its receipt without changing the latest decision")
		stored, err := repository.(AdjustmentReader).InspectAdjustment(t.Context(), original, adjustment.ID)
		require.NoError(t, err)
		require.Equal(t, adjustment, stored)
		totals, err := repository.Totals(t.Context(), GatewayScope(), IntervalDay, original.Timestamp)
		require.NoError(t, err)
		require.Equal(t, Totals{Requests: 2, Tokens: original.Tokens.Total, SpendNanoUSD: original.Cost.NanoUSD}, totals)
		changed := second
		changed.Cost.NanoUSD = 1
		changed.BillingDisposition = "administrator_usage"
		require.ErrorIs(t, writer.Adjust(t.Context(), changed), ErrRecordConflict)
		stale := adjustment
		stale.ID = "stale"
		require.ErrorIs(t, writer.Adjust(t.Context(), stale), ErrRecordConflict)
		foreign := original
		foreign.AccountID = "other-account"
		_, err = repository.(AdjustmentReader).InspectAdjustment(t.Context(), foreign, adjustment.ID)
		require.ErrorIs(t, err, ErrCorruptRecord)
	})
}

func TestUsageAdjustmentFailureIsAtomic(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprint(after), func(t *testing.T) {
			repotest.Run(t, func(t *testing.T, store storage.KVStore) {
				repository, err := Open(store, Options{})
				require.NoError(t, err)
				original := testRecord("key", "video", time.Now().UTC())
				original.Operation = OperationVideos
				require.NoError(t, repository.Put(t.Context(), original))
				broken, err := Open(&interruptedUsageStore{KVStore: store, after: after}, Options{})
				require.NoError(t, err)
				adjustment := adjustmentFixture(original, "decision", "", 10, 20)
				require.ErrorIs(t, broken.(AdjustmentWriter).Adjust(t.Context(), adjustment), errUsageWriteInterrupted)
				totals, err := repository.Totals(t.Context(), KeyScope("key"), IntervalDay, original.Timestamp)
				require.NoError(t, err)
				if after {
					require.Equal(t, Totals{Requests: 1, Tokens: 20, SpendNanoUSD: 10}, totals)
				} else {
					require.Equal(t, Totals{Requests: 1, Tokens: 150, SpendNanoUSD: 1250000}, totals)
				}
				reopened, err := Open(store, Options{})
				require.NoError(t, err)
				require.NoError(t, reopened.(AdjustmentWriter).Adjust(t.Context(), adjustment))
				totals, err = reopened.Totals(t.Context(), KeyScope("key"), IntervalDay, original.Timestamp)
				require.NoError(t, err)
				require.Equal(t, Totals{Requests: 1, Tokens: 20, SpendNanoUSD: 10}, totals)
			})
		})
	}
}

func TestConcurrentUsageAdjustments(t *testing.T) {
	for _, same := range []bool{false, true} {
		t.Run(fmt.Sprint(same), func(t *testing.T) {
			repotest.Run(t, func(t *testing.T, store storage.KVStore) {
				repository, err := Open(store, Options{})
				require.NoError(t, err)
				original := testRecord("key", "video", time.Now().UTC())
				original.Operation = OperationVideos
				require.NoError(t, repository.Put(t.Context(), original))
				var workers sync.WaitGroup
				results := make(chan error, 16)
				for i := range 16 {
					workers.Go(func() {
						id := "same"
						if !same {
							id = fmt.Sprint(i)
						}
						results <- repository.(AdjustmentWriter).Adjust(t.Context(), adjustmentFixture(original, id, "", 10, 20))
					})
				}
				workers.Wait()
				close(results)
				accepted := 0
				for err := range results {
					if err == nil {
						accepted++
					} else {
						require.ErrorIs(t, err, ErrRecordConflict)
					}
				}
				if same {
					require.Equal(t, 16, accepted)
				} else {
					require.Equal(t, 1, accepted)
				}
				totals, err := repository.Totals(t.Context(), GatewayScope(), IntervalDay, original.Timestamp)
				require.NoError(t, err)
				require.Equal(t, Totals{Requests: 1, Tokens: 20, SpendNanoUSD: 10}, totals)
			})
		})
	}
}

func TestUsageAdjustmentRefusesCorruptTotals(t *testing.T) {
	for _, value := range []string{"missing", "negative", "overflow"} {
		t.Run(value, func(t *testing.T) {
			repotest.Run(t, func(t *testing.T, store storage.KVStore) {
				repository, err := Open(store, Options{})
				require.NoError(t, err)
				original := testRecord("key", "video", time.Now().UTC())
				original.Operation = OperationVideos
				original.TeamID = "team"
				require.NoError(t, repository.Put(t.Context(), original))
				start, _ := window(IntervalMonth, original.Timestamp)
				counter := aggregateKey(TeamScope("team"), IntervalMonth, start, counterSpend)
				switch value {
				case "missing":
					require.NoError(t, store.Delete(t.Context(), counter))
				case "negative":
					require.NoError(t, store.Set(t.Context(), counter, []byte("-1")))
				case "overflow":
					require.NoError(t, store.Set(t.Context(), counter, []byte("9223372036854775807")))
				}
				adjustment := adjustmentFixture(original, "decision", "", original.Cost.NanoUSD+10, 20)
				require.Error(t, repository.(AdjustmentWriter).Adjust(t.Context(), adjustment))
				_, err = repository.(AdjustmentReader).InspectAdjustment(t.Context(), original, adjustment.ID)
				require.ErrorIs(t, err, storage.ErrNotFound)
				totals, err := repository.Totals(t.Context(), GatewayScope(), IntervalDay, original.Timestamp)
				require.NoError(t, err)
				require.Equal(t, Totals{Requests: 1, Tokens: 150, SpendNanoUSD: 1250000}, totals)
				page, err := repository.List(t.Context(), Query{})
				require.NoError(t, err)
				require.Nil(t, page.Records[0].BillingAdjustment)
			})
		})
	}
}

func TestUsageAdjustmentCannotReviveExpiredRecords(t *testing.T) {
	repotest.RunWithClock(t, func(t *testing.T, store storage.KVStore) {
		repository, err := Open(store, Options{Retention: time.Hour})
		require.NoError(t, err)
		original := testRecord("key", "video", time.Now().UTC())
		original.Operation = OperationVideos
		require.NoError(t, repository.Put(t.Context(), original))
		adjustment := adjustmentFixture(original, "decision", "", 10, 20)
		require.NoError(t, repository.(AdjustmentWriter).Adjust(t.Context(), adjustment))
		time.Sleep(2 * time.Hour)
		later := adjustmentFixture(original, "late", adjustment.ID, 0, 0)
		require.ErrorIs(t, repository.(AdjustmentWriter).Adjust(t.Context(), later), ErrRecordExpired)
	})
}

func TestUsageAdjustmentResolvesUnknownCostWithoutInventingMeasurements(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		repository, err := Open(store, Options{})
		require.NoError(t, err)
		original := testRecord("key", "video", time.Now().UTC())
		original.Operation = OperationVideos
		original.Cost = nil
		original.CostUnavailableReason = CostReasonNoUsage
		original.Tokens = Tokens{}
		original.TokensUnknown = true
		require.NoError(t, repository.Put(t.Context(), original))
		adjustment := adjustmentFixture(original, "decision", "", 70, 8)
		require.NoError(t, repository.(AdjustmentWriter).Adjust(t.Context(), adjustment))
		page, err := repository.List(t.Context(), Query{})
		require.NoError(t, err)
		require.Len(t, page.Records, 1)
		record := page.Records[0]
		require.EqualValues(t, 70, record.Cost.NanoUSD)
		require.Nil(t, record.BillingAdjustment.OriginalCost)
		require.Equal(t, CostReasonNoUsage, record.BillingAdjustment.OriginalCostUnavailableReason)
		require.True(t, record.BillingAdjustment.OriginalTokensUnknown)
		require.Nil(t, record.Media)
		require.Equal(t, "administrator_usage", record.BillingDisposition)
		require.False(t, record.TokensUnknown)
		totals, err := repository.Totals(t.Context(), GatewayScope(), IntervalDay, original.Timestamp)
		require.NoError(t, err)
		require.Equal(t, Totals{Requests: 1, Tokens: 8, SpendNanoUSD: 70}, totals)
	})
}

func TestUsageAdjustmentCorruptionDoesNotAffectOtherAccounts(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		repository, err := Open(store, Options{})
		require.NoError(t, err)
		original := testRecord("key-a", "video", time.Now().UTC())
		original.Operation = OperationVideos
		original.AccountID = "account-a"
		other := original
		other.KeyID = "key-b"
		other.AccountID = "account-b"
		require.NoError(t, repository.Put(t.Context(), original))
		require.NoError(t, repository.Put(t.Context(), other))
		require.NoError(t, repository.(AdjustmentWriter).Adjust(t.Context(), adjustmentFixture(original, "decision", "", 10, 20)))
		key := recordKey(original.KeyID, original.Timestamp, original.RequestID)
		require.NoError(t, store.Set(t.Context(), adjustmentHeadKey(key), []byte("invalid")))
		_, err = repository.List(t.Context(), Query{AccountID: original.AccountID})
		require.ErrorIs(t, err, ErrCorruptRecord)
		page, err := repository.List(t.Context(), Query{AccountID: other.AccountID})
		require.NoError(t, err)
		require.Len(t, page.Records, 1)
		require.Nil(t, page.Records[0].BillingAdjustment)
		require.Equal(t, other.Cost, page.Records[0].Cost)
	})
}
