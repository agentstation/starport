package usage

import (
	"context"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

// An account spend budget bounds a turn only through the cost on its record. A
// media turn whose cost is missing therefore spends nothing against the cap, so
// the two facts below are one fact: what the record carries is what the budget
// meters.

func mediaRecord(requestID string, at time.Time) Record {
	record := testRecord("key-a", requestID, at)
	record.Tokens = Tokens{Input: 1200, Output: 300, Total: 1500, AudioInput: 600, AudioOutput: 200}
	record.Media = &Media{GeneratedImages: 2}
	record.Cost = &Cost{NanoUSD: 84_000_000, Currency: "USD"}
	return record
}

// TestAPricedMediaTurnCountsAgainstTheSpendBudget is the acceptance case. The
// counter this reads is the one internal/server subtracts from an account spend
// limit, so a media turn that reached it as zero would be unbounded.
func TestAPricedMediaTurnCountsAgainstTheSpendBudget(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store, Options{})
		require.NoError(t, err)

		require.NoError(t, repository.Put(ctx, mediaRecord("req-media", testBase)))

		totals, err := repository.Totals(ctx, KeyScope("key-a"), IntervalDay, testBase)
		require.NoError(t, err)
		require.EqualValues(t, 84_000_000, totals.SpendNanoUSD,
			"the media turn spent nothing against the budget")
		require.EqualValues(t, 1500, totals.Tokens)
	})
}

// TestAnUnpricedMediaTurnSpendsNothingAndSaysSo states the other outcome. The
// gateway cannot invent a price it does not have, so an unpriced media turn
// does spend zero. It has to arrive with the reason attached, or an operator
// reading the record cannot tell a free turn from an unknown one.
func TestAnUnpricedMediaTurnSpendsNothingAndSaysSo(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store, Options{})
		require.NoError(t, err)

		record := mediaRecord("req-unpriced", testBase)
		record.Cost = nil
		record.CostUnavailableReason = CostReasonMediaUnpriced
		require.NoError(t, repository.Put(ctx, record))

		totals, err := repository.Totals(ctx, KeyScope("key-a"), IntervalDay, testBase)
		require.NoError(t, err)
		require.Zero(t, totals.SpendNanoUSD)

		page, err := repository.List(ctx, Query{KeyID: "key-a"})
		require.NoError(t, err)
		require.Len(t, page.Records, 1)
		require.Equal(t, CostReasonMediaUnpriced, page.Records[0].CostUnavailableReason)
	})
}

// TestMediaUnitsSurviveTheRoundTrip holds the stored shape. The console reads
// these counts back out of a stored record rather than off a live response.
func TestMediaUnitsSurviveTheRoundTrip(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store, Options{})
		require.NoError(t, err)

		require.NoError(t, repository.Put(ctx, mediaRecord("req-media", testBase)))
		page, err := repository.List(ctx, Query{KeyID: "key-a"})
		require.NoError(t, err)
		require.Len(t, page.Records, 1)

		got := page.Records[0]
		require.NotNil(t, got.Media)
		require.EqualValues(t, 2, got.Media.GeneratedImages)
		require.EqualValues(t, 600, got.Tokens.AudioInput)
		require.EqualValues(t, 200, got.Tokens.AudioOutput)
	})
}

// TestATextRecordReadsBackWithoutMedia states the compatibility rule. Every
// record written before media accounting existed has no media object, and it
// has to read back as absent rather than as a turn that generated nothing.
func TestATextRecordReadsBackWithoutMedia(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store, Options{})
		require.NoError(t, err)

		require.NoError(t, repository.Put(ctx, testRecord("key-a", "req-text", testBase)))
		page, err := repository.List(ctx, Query{KeyID: "key-a"})
		require.NoError(t, err)
		require.Len(t, page.Records, 1)
		require.Nil(t, page.Records[0].Media)
		require.Zero(t, page.Records[0].Tokens.AudioInput)
	})
}
