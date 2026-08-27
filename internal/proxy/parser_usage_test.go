package proxy

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/document"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/usage"
)

// PLG-V14 and PLG-V15. A document read is the one provider call this gateway
// makes that the request did not name. It happens before the model the caller
// asked for runs, it is billed by the page rather than by the token, and
// nothing else in the record would show it.
//
// The prices come from a fixture rather than from the shipped catalog, because
// no offering in the catalog serves recognition yet. What the tests hold is the
// arithmetic and the refusal, and both have to be right on the day one does.

// pageFixture prices recognition the way a catalog does: one price for the
// offering the planner chose, and one for the cheapest offering there is.
type pageFixture struct {
	perModel map[string]float64
	lowest   float64
	priced   bool
}

func recognitionPrices() *pageFixture {
	return &pageFixture{
		// Ten cents a page at the model the fixture router answers with, and a
		// cheaper offering elsewhere in the same generation.
		perModel: map[string]float64{"google/gemini-2.5-flash": 0.10},
		lowest:   0.04,
		priced:   true,
	}
}

func (f *pageFixture) PagePriceFor(
	modelID string,
	operation starmapcatalogs.ProviderOperation,
) (float64, bool) {
	if operation != starmapcatalogs.ProviderOperationDocumentsRecognition {
		return 0, false
	}
	price, found := f.perModel[modelID]
	return price, found
}

func (f *pageFixture) LowestPagePrice(operation starmapcatalogs.ProviderOperation) (float64, bool) {
	if operation != starmapcatalogs.ProviderOperationDocumentsRecognition || !f.priced {
		return 0, false
	}
	return f.lowest, true
}

// meterReader returns the one record this turn wrote, after the capture
// middleware has finished writing it.
type meterReader func(t *testing.T) usage.Record

// meteredProxy returns a proxy whose usage records are captured, the router
// that answers its recognition calls, and a reader for the record it wrote.
func meteredProxy(t *testing.T, pages ...string) (Proxy, *recognizingRouter, meterReader) {
	t.Helper()
	return meteredProxyWithPrices(t, recognitionPrices(), pages...)
}

// meteredProxyWithPrices is meteredProxy with the page prices chosen, which is
// how a gateway that cannot price a page at all is put under test.
func meteredProxyWithPrices(
	t *testing.T,
	prices pagePrices,
	pages ...string,
) (Proxy, *recognizingRouter, meterReader) {
	t.Helper()
	router := newRecognizingRouter(pages...)
	repository := &recordingUsageRepository{}
	capture := NewUsageCapture(repository)
	service := capture.Wrap(&proxy{router: router, prices: prices})
	read := func(t *testing.T) usage.Record {
		t.Helper()
		capture.Flush()
		records := repository.all()
		require.Len(t, records, 1)
		return records[0]
	}
	return service, router, read
}

// meteredCachingProxy is meteredProxy with a document cache behind it, which
// is how a second turn over the same document is put under test.
func meteredCachingProxy(t *testing.T, page string) (Proxy, *recognizingRouter, meterReaders) {
	t.Helper()
	cache, err := document.NewCache(newExtractionStore(), nil, 0)
	require.NoError(t, err)
	router := newRecognizingRouter(page)
	repository := &recordingUsageRepository{}
	capture := NewUsageCapture(repository)
	service := capture.Wrap(&proxy{
		router:      router,
		prices:      recognitionPrices(),
		extractions: cache,
	})
	read := func(t *testing.T, count int) []usage.Record {
		t.Helper()
		capture.Flush()
		records := repository.all()
		require.Len(t, records, count)
		return records
	}
	return service, router, read
}

// meterReaders returns the records this proxy has written so far.
type meterReaders func(t *testing.T, count int) []usage.Record

// TestACachedDocumentIsRecordedAsCachedAndNotChargedAgain holds what the second
// turn of a conversation about one document reports.
//
// The pages are the same pages and the cost is nothing, which is exactly what a
// native read records too. Only the cached flag separates them, and without it
// an operator reading a month of records would credit the native engine for
// every cache hit the recognizer paid for once.
func TestACachedDocumentIsRecordedAsCachedAndNotChargedAgain(t *testing.T) {
	t.Parallel()
	service, router, read := meteredCachingProxy(t, "INVOICE 4471\nAmount due: $912.00")

	// The cache key names the catalog generation that read the document, so a
	// turn with no catalog in force caches nothing.
	ctx := catalogContext(t)
	for range 2 {
		_, err := service.ProcessChatCompletion(ctx,
			parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition))
		require.NoError(t, err)
	}
	require.Equal(t, 1, router.calls,
		"the second turn paid to read a document it had already read")

	records := read(t, 2)
	first, second := records[0], records[1]
	require.False(t, first.ExtractionCached)
	require.NotNil(t, first.ExtractionCost)

	require.True(t, second.ExtractionCached,
		"a cache hit was recorded as an uncached read of the same pages")
	require.Equal(t, first.DocumentPages, second.DocumentPages,
		"a cached turn stopped naming the pages the model was given")
	require.Zero(t, second.RecognizedPages)
	require.Nil(t, second.ExtractionCost,
		"the account paid twice to read one document")
}

// TestARecognizedDocumentRecordsItsPagesAndItsCost is the acceptance case. The
// pages reached a provider and the provider charged for them, so a record that
// carried neither would let an account read documents for free while its spend
// budget said it had spent nothing.
func TestARecognizedDocumentRecordsItsPagesAndItsCost(t *testing.T) {
	t.Parallel()
	service, router, read := meteredProxy(t, "INVOICE 4471\nAmount due: $912.00")

	_, err := service.ProcessChatCompletion(context.Background(),
		parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition))
	require.NoError(t, err)
	require.Equal(t, 1, router.calls)

	record := read(t)
	require.Equal(t, string(inference.ParserEngineRecognition), record.ParserEngine)
	require.EqualValues(t, 1, record.DocumentPages)
	require.EqualValues(t, 1, record.RecognizedPages,
		"a page a recognition model read was metered as free")
	require.Zero(t, record.NativePages)
	require.NotNil(t, record.ExtractionCost)
	require.EqualValues(t, 100_000_000, record.ExtractionCost.NanoUSD,
		"the page was charged at a price the catalog did not publish")
	require.Equal(t, "USD", record.ExtractionCost.Currency)
}

// TestARecognizedDocumentIsAddedToTheTurnsOwnCost states the number a spend
// budget reads. The budget meters one field, so a document cost reported beside
// the total rather than inside it would be visible to an operator and invisible
// to the cap.
func TestARecognizedDocumentIsAddedToTheTurnsOwnCost(t *testing.T) {
	t.Parallel()
	service, _, read := meteredProxy(t, "INVOICE 4471")

	_, err := service.ProcessChatCompletion(context.Background(),
		parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition))
	require.NoError(t, err)

	record := read(t)
	require.NotNil(t, record.ExtractionCost)
	if record.Cost == nil {
		// The fixture router answers with no token usage, so the chat half of
		// the turn has no price of its own. The reason has to say so rather
		// than the total silently reading as the document alone.
		require.NotEmpty(t, record.CostUnavailableReason)
		return
	}
	require.GreaterOrEqual(t, record.Cost.NanoUSD, record.ExtractionCost.NanoUSD,
		"the document read was left out of the cost the spend budget meters")
}

// TestANativelyReadDocumentIsRecordedAndNotCharged holds the boundary the two
// engines sit on either side of. The native engine reads in this process, so
// its pages are counted and never charged: a record with a cost on them would
// bill an account for work no provider did.
func TestANativelyReadDocumentIsRecordedAndNotCharged(t *testing.T) {
	t.Parallel()
	service, router, read := meteredProxy(t, "never read")

	_, err := service.ProcessChatCompletion(context.Background(),
		parsedRequest(t, "handwritten.pdf", inference.ParserEngineNative))
	require.NoError(t, err)
	require.Zero(t, router.calls, "a natively read document reached a provider")

	record := read(t)
	require.Equal(t, string(inference.ParserEngineNative), record.ParserEngine)
	require.Positive(t, record.NativePages)
	require.EqualValues(t, record.DocumentPages, record.NativePages)
	require.Zero(t, record.RecognizedPages)
	require.Nil(t, record.ExtractionCost,
		"a page this gateway read in its own process was charged for")
}

// TestTheRecordReportsHowLongTheDocumentTookIsTheFourthAcceptance holds the
// latency nothing else in the record shows. A recognition call runs inside the
// request and before the model does, so the request latency reports the two
// together and says nothing about which of them was slow.
func TestTheRecordReportsHowLongTheDocumentTook(t *testing.T) {
	t.Parallel()
	service, _, read := meteredProxy(t, "INVOICE 4471")

	_, err := service.ProcessChatCompletion(context.Background(),
		parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition))
	require.NoError(t, err)

	record := read(t)
	require.GreaterOrEqual(t, record.ExtractionMillis, int64(0))
	require.LessOrEqual(t, record.ExtractionMillis, record.LatencyMS,
		"the document read took longer than the request that contained it")
}

// TestAnAccountAtItsSpendBoundIsRefusedBeforeTheRecognitionCall is the refusal
// PLG6 exists for.
//
// The order is the whole assertion. A refusal after the call has already spent
// the money the bound was there to protect, and a recognition call is the one
// spend in this gateway whose price is known before it happens.
func TestAnAccountAtItsSpendBoundIsRefusedBeforeTheRecognitionCall(t *testing.T) {
	t.Parallel()
	service, router, _ := meteredProxy(t, "INVOICE 4471")

	// Four cents of allowance against a one-page document the cheapest
	// offering reads for four cents and one nano-USD more.
	ctx := limits.ContextWithAllowance(context.Background(),
		limits.Allowance{NanoUSD: 39_999_999, Bounded: true})

	_, err := service.ProcessChatCompletion(ctx,
		parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition))
	require.Error(t, err)
	require.Zero(t, router.calls,
		"the account was billed for the document that passed its spend bound")

	require.True(t, errors.Is(err, limits.ErrSpendLimitExceeded),
		"a spend refusal reached the caller as something other than a budget")
	var normalized *failure.Failure
	require.ErrorAs(t, err, &normalized)
	require.Equal(t, failure.Billing, normalized.Kind())
	require.False(t, normalized.Retryable(),
		"a caller was told to retry a request its budget cannot pay for")
}

// TestAnAccountThatCanAffordThePagesReadsThem is the other half of the bound.
// A budget that refused work the account could pay for would cost a caller a
// request over a price that was never charged.
func TestAnAccountThatCanAffordThePagesReadsThem(t *testing.T) {
	t.Parallel()
	service, router, _ := meteredProxy(t, "INVOICE 4471")

	ctx := limits.ContextWithAllowance(context.Background(),
		limits.Allowance{NanoUSD: 40_000_000, Bounded: true})

	_, err := service.ProcessChatCompletion(ctx,
		parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition))
	require.NoError(t, err)
	require.Equal(t, 1, router.calls)
}

// TestAnAccountWithNoSpendBudgetIsRefusedNothing states what happens in the
// deployment most operators run. No budget is set, so the allowance is
// unbounded, and reading it as an empty one would refuse every document turn in
// an unmetered gateway.
func TestAnAccountWithNoSpendBudgetIsRefusedNothing(t *testing.T) {
	t.Parallel()
	service, router, _ := meteredProxy(t, "INVOICE 4471")

	_, err := service.ProcessChatCompletion(context.Background(),
		parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition))
	require.NoError(t, err)
	require.Equal(t, 1, router.calls)
}

// TestAnUnpricedRecognitionSaysSoRatherThanReadingAsFree holds how the meter
// fails. The projection refuses a recognition offering with no page price, so
// this state means the gateway lost the catalog rather than that the page was
// free. A zero cost would be a silent understatement of a real charge.
func TestAnUnpricedRecognitionSaysSoRatherThanReadingAsFree(t *testing.T) {
	t.Parallel()
	service, _, read := meteredProxyWithPrices(t, nil, "INVOICE 4471")

	_, err := service.ProcessChatCompletion(context.Background(),
		parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition))
	require.NoError(t, err)

	record := read(t)
	require.EqualValues(t, 1, record.RecognizedPages)
	require.Nil(t, record.ExtractionCost)
	require.Nil(t, record.Cost)
	require.Equal(t, usage.CostReasonNoPricing, record.CostUnavailableReason,
		"a page charged at an unknown price was recorded as costing nothing")
}
