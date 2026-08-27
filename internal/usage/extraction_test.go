package usage

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
)

// Reading a document is a provider call inside a provider call. It happens
// before the model the request came for runs, it is billed by the page rather
// than by the token, and the account owes it either way.
//
// These tests state what a record has to carry for that to be true: the pages
// count, the cost is inside the total the spend budget meters, and the
// recognized share stays readable on its own.

func extractionRecord(requestID string, at time.Time) Record {
	record := testRecord("key-a", requestID, at)
	record.Tokens = Tokens{Input: 900, Output: 200, Total: 1100}
	record.ParserEngine = "recognition"
	record.DocumentPages = 12
	record.RecognizedPages = 12
	record.ExtractionMillis = 3400
	record.ExtractionCost = &Cost{NanoUSD: 60_000_000, Currency: "USD"}
	record.Cost = &Cost{NanoUSD: 71_000_000, Currency: "USD"}
	return record
}

// TestARecognizedDocumentCountsAgainstTheSpendBudget is the acceptance case.
// The counter this reads is the one internal/server subtracts from a spend
// limit. A recognition call that reached it as zero would let an account read
// documents without bound while its budget said it had spent nothing.
func TestARecognizedDocumentCountsAgainstTheSpendBudget(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store, Options{})
		require.NoError(t, err)

		require.NoError(t, repository.Put(ctx, extractionRecord("req-ocr", testBase)))

		totals, err := repository.Totals(ctx, KeyScope("key-a"), IntervalDay, testBase)
		require.NoError(t, err)
		require.EqualValues(t, 71_000_000, totals.SpendNanoUSD,
			"the pages a recognition model read spent nothing against the budget")
	})
}

// TestARecognizedDocumentReportsItsPagesAndItsCost holds the two numbers an
// operator needs to answer why a bill moved. The page count says how much
// document was read, and the extraction cost says what that share of the turn
// cost apart from the answer the model gave about it.
func TestARecognizedDocumentReportsItsPagesAndItsCost(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store, Options{})
		require.NoError(t, err)

		require.NoError(t, repository.Put(ctx, extractionRecord("req-ocr", testBase)))

		page, err := repository.List(ctx, Query{KeyID: "key-a"})
		require.NoError(t, err)
		require.Len(t, page.Records, 1)
		stored := page.Records[0]

		require.EqualValues(t, 12, stored.RecognizedPages)
		require.EqualValues(t, 12, stored.DocumentPages)
		require.Equal(t, "recognition", stored.ParserEngine)
		require.NotNil(t, stored.ExtractionCost)
		require.EqualValues(t, 60_000_000, stored.ExtractionCost.NanoUSD)
		require.Less(t, stored.ExtractionCost.NanoUSD, stored.Cost.NanoUSD,
			"the document read was reported as the whole turn")
	})
}

// TestANativelyReadDocumentCostsNothing states the boundary the two engines
// sit on either side of. The native engine reaches no provider, so its pages
// are counted and never charged. A record that carried a cost for them would
// bill an account for work no provider did.
func TestANativelyReadDocumentCostsNothing(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store, Options{})
		require.NoError(t, err)

		record := extractionRecord("req-native", testBase)
		record.ParserEngine = "native"
		record.RecognizedPages = 0
		record.NativePages = 12
		record.ExtractionCost = nil
		record.Cost = &Cost{NanoUSD: 11_000_000, Currency: "USD"}
		require.NoError(t, repository.Put(ctx, record))

		page, err := repository.List(ctx, Query{KeyID: "key-a"})
		require.NoError(t, err)
		require.Len(t, page.Records, 1)
		stored := page.Records[0]

		require.EqualValues(t, 12, stored.NativePages)
		require.EqualValues(t, 12, stored.DocumentPages)
		require.Zero(t, stored.RecognizedPages)
		require.Nil(t, stored.ExtractionCost,
			"a document nothing was paid to read carried a price")

		totals, err := repository.Totals(ctx, KeyScope("key-a"), IntervalDay, testBase)
		require.NoError(t, err)
		require.EqualValues(t, 11_000_000, totals.SpendNanoUSD)
	})
}

// TestTheExtractionDurationSurvivesTheRoundTrip holds the latency an operator
// cannot find anywhere else. A recognition call runs inside the request and
// before the model does, so the request latency reports the two together and
// says nothing about which one was slow.
func TestTheExtractionDurationSurvivesTheRoundTrip(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store, Options{})
		require.NoError(t, err)

		require.NoError(t, repository.Put(ctx, extractionRecord("req-ocr", testBase)))

		page, err := repository.List(ctx, Query{KeyID: "key-a"})
		require.NoError(t, err)
		require.Len(t, page.Records, 1)
		require.EqualValues(t, 3400, page.Records[0].ExtractionMillis)
	})
}

// TestATurnThatAttachedNoDocumentReportsNoExtraction states what the fields
// read as on every other request the gateway serves. They are absent, not zero:
// a chat turn with no attachment did not read a document of no pages.
func TestATurnThatAttachedNoDocumentReportsNoExtraction(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store, Options{})
		require.NoError(t, err)

		record := testRecord("key-a", "req-plain", testBase)
		record.Cost = &Cost{NanoUSD: 4_000_000, Currency: "USD"}
		require.NoError(t, repository.Put(ctx, record))

		page, err := repository.List(ctx, Query{KeyID: "key-a"})
		require.NoError(t, err)
		require.Len(t, page.Records, 1)
		stored := page.Records[0]

		require.Empty(t, stored.ParserEngine)
		require.Zero(t, stored.DocumentPages)
		require.Zero(t, stored.RecognizedPages)
		require.Zero(t, stored.ExtractionMillis)
		require.Nil(t, stored.ExtractionCost)
	})
}
