package usage

import (
	"context"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

var testBase = time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

func testRecord(keyID, requestID string, at time.Time) Record {
	return Record{
		RequestID:      requestID,
		KeyID:          keyID,
		Timestamp:      at,
		Protocol:       "openrouter",
		Operation:      "chat",
		ModelRequested: "openai/gpt-4o",
		ModelUsed:      "openai/gpt-4o",
		Provider:       "openai",
		Status:         StatusOK,
		StatusCode:     200,
		Tokens:         Tokens{Input: 100, Output: 50, Total: 150, CacheRead: 10},
		LatencyMS:      420,
		Attempts:       1,
		Cost:           &Cost{NanoUSD: 1_250_000, Currency: "USD"},
	}
}

func TestRepositoryPutAndListByKey(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store, Options{})
		require.NoError(t, err)

		mine := testRecord("key-a", "req-1", testBase)
		require.NoError(t, repository.Put(ctx, mine))
		require.NoError(t, repository.Put(ctx, testRecord("key-b", "req-2", testBase.Add(time.Second))))

		page, err := repository.List(ctx, Query{KeyID: "key-a"})
		require.NoError(t, err)
		require.Len(t, page.Records, 1)
		require.Empty(t, page.NextCursor)

		got := page.Records[0]
		require.Equal(t, mine.RequestID, got.RequestID)
		require.Equal(t, mine.KeyID, got.KeyID)
		require.True(t, mine.Timestamp.Equal(got.Timestamp))
		require.Equal(t, mine.Tokens, got.Tokens)
		require.NotNil(t, got.Cost)
		require.Equal(t, int64(1_250_000), got.Cost.NanoUSD)
		require.Equal(t, "USD", got.Cost.Currency)
	})
}

func TestListByKeyOrdersNewestFirstAcrossBackends(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store, Options{})
		require.NoError(t, err)

		// Insertion order deliberately differs from time order.
		for _, offset := range []time.Duration{3 * time.Second, time.Second, 4 * time.Second, 2 * time.Second} {
			record := testRecord("key-a", "req-"+offset.String(), testBase.Add(offset))
			require.NoError(t, repository.Put(ctx, record))
		}

		page, err := repository.List(ctx, Query{KeyID: "key-a"})
		require.NoError(t, err)
		require.Len(t, page.Records, 4)
		for i := 1; i < len(page.Records); i++ {
			require.True(t, page.Records[i-1].Timestamp.After(page.Records[i].Timestamp),
				"records must order newest-first")
		}
	})
}

func TestListFiltersAndCursorPagination(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store, Options{})
		require.NoError(t, err)

		for i := range 10 {
			record := testRecord("key-a", "req-"+string(rune('a'+i)), testBase.Add(time.Duration(i)*time.Minute))
			if i%2 == 1 {
				record.Provider = "groq"
				record.ModelUsed = "meta-llama/llama-3.3-70b"
				record.Status = StatusError
				record.StatusCode = 502
			}
			require.NoError(t, repository.Put(ctx, record))
		}

		filtered, err := repository.List(ctx, Query{KeyID: "key-a", Provider: "groq", Status: StatusError})
		require.NoError(t, err)
		require.Len(t, filtered.Records, 5)
		for _, record := range filtered.Records {
			require.Equal(t, "groq", record.Provider)
			require.Equal(t, StatusError, record.Status)
		}

		ranged, err := repository.List(ctx, Query{
			KeyID: "key-a",
			Since: testBase.Add(2 * time.Minute),
			Until: testBase.Add(5 * time.Minute),
		})
		require.NoError(t, err)
		require.Len(t, ranged.Records, 4)

		// Walk every record in pages of three without overlap or loss.
		seen := map[string]bool{}
		cursor := ""
		for {
			page, err := repository.List(ctx, Query{KeyID: "key-a", Limit: 3, Cursor: cursor})
			require.NoError(t, err)
			for _, record := range page.Records {
				require.False(t, seen[record.RequestID], "no request may repeat across pages")
				seen[record.RequestID] = true
			}
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
		require.Len(t, seen, 10)
	})
}

func TestAdminListSpansKeys(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store, Options{})
		require.NoError(t, err)

		require.NoError(t, repository.Put(ctx, testRecord("key-a", "req-1", testBase)))
		require.NoError(t, repository.Put(ctx, testRecord("key-b", "req-2", testBase.Add(time.Second))))

		page, err := repository.List(ctx, Query{})
		require.NoError(t, err)
		require.Len(t, page.Records, 2)
		require.Equal(t, "key-b", page.Records[0].KeyID)
		require.Equal(t, "key-a", page.Records[1].KeyID)
	})
}

func TestAggregateCountersAccumulate(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store, Options{})
		require.NoError(t, err)

		require.NoError(t, repository.Put(ctx, testRecord("key-a", "req-1", testBase)))
		second := testRecord("key-a", "req-2", testBase.Add(time.Minute))
		second.Tokens = Tokens{Input: 10, Output: 40, Total: 50}
		second.Cost = &Cost{NanoUSD: 750_000, Currency: "USD"}
		require.NoError(t, repository.Put(ctx, second))
		require.NoError(t, repository.Put(ctx, testRecord("key-b", "req-3", testBase)))

		for _, interval := range []string{IntervalDay, IntervalWeek, IntervalMonth} {
			totals, err := repository.Totals(ctx, KeyScope("key-a"), interval, testBase)
			require.NoError(t, err)
			require.Equal(t, Totals{Requests: 2, Tokens: 200, SpendNanoUSD: 2_000_000}, totals, interval)
		}

		all, err := repository.Totals(ctx, GatewayScope(), IntervalDay, testBase)
		require.NoError(t, err)
		require.Equal(t, Totals{Requests: 3, Tokens: 350, SpendNanoUSD: 3_250_000}, all)

		// A different window is empty.
		empty, err := repository.Totals(ctx, KeyScope("key-a"), IntervalDay, testBase.AddDate(0, 0, -2))
		require.NoError(t, err)
		require.Equal(t, Totals{}, empty)
	})
}

func TestCostlessRecordCarriesReasonNotZero(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store, Options{})
		require.NoError(t, err)

		record := testRecord("key-a", "req-1", testBase)
		record.Cost = nil
		record.CostUnavailableReason = CostReasonNoPricing
		require.NoError(t, repository.Put(ctx, record))

		page, err := repository.List(ctx, Query{KeyID: "key-a"})
		require.NoError(t, err)
		require.Len(t, page.Records, 1)
		require.Nil(t, page.Records[0].Cost)
		require.Equal(t, CostReasonNoPricing, page.Records[0].CostUnavailableReason)

		totals, err := repository.Totals(ctx, KeyScope("key-a"), IntervalDay, testBase)
		require.NoError(t, err)
		require.Equal(t, Totals{Requests: 1, Tokens: 150}, totals)
	})
}

func TestRetentionTTLApplied(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		retention := 48 * time.Hour
		repository, err := Open(store, Options{Retention: retention})
		require.NoError(t, err)

		record := testRecord("key-a", "req-1", time.Now().UTC())
		require.NoError(t, repository.Put(ctx, record))

		ttl, err := store.GetTTL(ctx, recordKey(record.KeyID, record.Timestamp, record.RequestID))
		require.NoError(t, err)
		require.Greater(t, ttl, time.Duration(0))
		require.LessOrEqual(t, ttl, retention)

		start, end := window(IntervalDay, record.Timestamp)
		counterTTL, err := store.GetTTL(ctx, aggregateKey(KeyScope(record.KeyID), IntervalDay, start, "requests"))
		require.NoError(t, err)
		require.Greater(t, counterTTL, time.Duration(0))
		require.LessOrEqual(t, counterTTL, time.Until(end.Add(retention))+time.Minute)
	})
}

func TestPutRejectsInvalidRecords(t *testing.T) {
	repository, err := Open(storage.NewMockStore(), Options{})
	require.NoError(t, err)
	ctx := context.Background()

	missingCost := testRecord("key-a", "req-1", testBase)
	missingCost.Cost = nil
	missingCost.CostUnavailableReason = ""
	require.ErrorIs(t, repository.Put(ctx, missingCost), ErrInvalidRecord)

	badStatus := testRecord("key-a", "req-1", testBase)
	badStatus.Status = "weird"
	require.ErrorIs(t, repository.Put(ctx, badStatus), ErrInvalidRecord)

	noKey := testRecord("", "req-1", testBase)
	require.ErrorIs(t, repository.Put(ctx, noKey), ErrInvalidRecord)

	_, err = repository.Totals(ctx, KeyScope("key-a"), "hour", testBase)
	require.ErrorIs(t, err, ErrInvalidInterval)

	// A scope that names no subject reads no counter set. Answering it with
	// the gateway total would report every account's spend as one account's.
	_, err = repository.Totals(ctx, AccountScope(""), IntervalDay, testBase)
	require.ErrorIs(t, err, ErrInvalidScope)
}

// TestAccountCounterSumsEveryKeyItHolds proves the storage guarantee the
// account meter rests on. An account cap has to count every key the account
// holds, and a key counter cannot answer for it: two keys under one account
// each stay well under their own totals while the account total is their sum.
func TestAccountCounterSumsEveryKeyItHolds(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store, Options{})
		require.NoError(t, err)

		for _, spec := range []struct{ keyID, requestID, accountID string }{
			{"key-a", "req-1", "acme"},
			{"key-b", "req-2", "acme"},
			{"key-c", "req-3", "globex"},
		} {
			record := testRecord(spec.keyID, spec.requestID, testBase)
			record.AccountID = spec.accountID
			require.NoError(t, repository.Put(ctx, record))
		}

		perKey, err := repository.Totals(ctx, KeyScope("key-a"), IntervalDay, testBase)
		require.NoError(t, err)
		require.Equal(t, Totals{Requests: 1, Tokens: 150, SpendNanoUSD: 1_250_000}, perKey)

		account, err := repository.Totals(ctx, AccountScope("acme"), IntervalDay, testBase)
		require.NoError(t, err)
		require.Equal(t, Totals{Requests: 2, Tokens: 300, SpendNanoUSD: 2_500_000}, account,
			"the account total must be the sum over every key it holds")

		other, err := repository.Totals(ctx, AccountScope("globex"), IntervalDay, testBase)
		require.NoError(t, err)
		require.Equal(t, Totals{Requests: 1, Tokens: 150, SpendNanoUSD: 1_250_000}, other,
			"one account's traffic must not reach another account's counter")
	})
}

// TestListByAccountSpansEveryKey covers the per-provider rollup's read path:
// records are key-indexed, so an account query has to scan and filter rather
// than address a namespace, and it must still return every key's records.
func TestListByAccountSpansEveryKey(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store, Options{})
		require.NoError(t, err)

		for _, spec := range []struct{ keyID, requestID, accountID string }{
			{"key-a", "req-1", "acme"},
			{"key-b", "req-2", "acme"},
			{"key-c", "req-3", "globex"},
		} {
			record := testRecord(spec.keyID, spec.requestID, testBase)
			record.AccountID = spec.accountID
			require.NoError(t, repository.Put(ctx, record))
		}

		page, err := repository.List(ctx, Query{AccountID: "acme"})
		require.NoError(t, err)
		require.Len(t, page.Records, 2)

		keys := []string{page.Records[0].KeyID, page.Records[1].KeyID}
		require.ElementsMatch(t, []string{"key-a", "key-b"}, keys)
	})
}
